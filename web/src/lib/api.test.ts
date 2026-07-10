import { describe, expect, it } from "vitest";
import { ApiError, api, buildUrl } from "@/lib/api";
import { authStore } from "@/lib/auth";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import {
  authResponse,
  rawLeaderboards,
  rawTimeline,
  rawToken,
} from "@/test/factories";

describe("buildUrl (P0)", () => {
  it("returns the path unchanged with no params", () => {
    expect(buildUrl("/x")).toBe("/x");
    expect(buildUrl("/x", {})).toBe("/x");
  });

  it("drops undefined/null/'' but keeps 0", () => {
    expect(
      buildUrl("/x", { a: undefined, b: null, c: "", page: 0, limit: 50 }),
    ).toBe("/x?page=0&limit=50");
  });

  it("URL-encodes values", () => {
    expect(buildUrl("/x", { q: "a b&c" })).toBe("/x?q=a+b%26c");
  });

  it("no trailing ? when all params drop out", () => {
    expect(buildUrl("/x", { a: undefined, b: "" })).toBe("/x");
  });
});

describe("api normalizers (msw, raw hakatime shapes) — P0", () => {
  it("getTokens maps tknId->id, tknName->name, tknDesc->desc", async () => {
    server.use(
      http.get("/auth/tokens", () =>
        HttpResponse.json([
          rawToken({ tknId: "abc", tknName: "n", tknDesc: "d", lastUsage: null }),
        ]),
      ),
    );
    const tokens = await api.getTokens();
    expect(tokens).toEqual([
      { id: "abc", name: "n", desc: "d", lastUsage: null },
    ]);
  });

  it("getTimeline maps timelineLangs -> langs and t* keys", async () => {
    server.use(
      http.get("/api/v1/users/current/timeline", () =>
        HttpResponse.json(
          rawTimeline({
            Go: [
              { tName: "Go", tRangeStart: "2026-07-09T10:00:00Z", tRangeEnd: "2026-07-09T11:00:00Z" },
            ],
          }),
        ),
      ),
    );
    const tl = await api.getTimeline({ start: "a", end: "b" });
    expect(tl.langs.Go).toEqual([
      { name: "Go", rangeStart: "2026-07-09T10:00:00Z", rangeEnd: "2026-07-09T11:00:00Z" },
    ]);
  });

  it("getTimeline: absent timelineLangs -> {} langs", async () => {
    server.use(
      http.get("/api/v1/users/current/timeline", () => HttpResponse.json({})),
    );
    const tl = await api.getTimeline({ start: "a", end: "b" });
    expect(tl.langs).toEqual({});
  });

  it("getLeaderboards maps lang -> languages", async () => {
    server.use(
      http.get("/api/v1/leaderboards", () =>
        HttpResponse.json(
          rawLeaderboards({
            global: [{ name: "x", value: 10 }],
            lang: { Go: [{ name: "x", value: 5 }] },
          }),
        ),
      ),
    );
    const lb = await api.getLeaderboards({ start: "a", end: "b" });
    expect(lb.global).toEqual([{ name: "x", value: 10 }]);
    expect(lb.languages).toEqual({ Go: [{ name: "x", value: 5 }] });
  });

  it("getLeaderboards: missing keys -> [] / {}", async () => {
    server.use(
      http.get("/api/v1/leaderboards", () => HttpResponse.json({})),
    );
    const lb = await api.getLeaderboards({ start: "a", end: "b" });
    expect(lb.global).toEqual([]);
    expect(lb.languages).toEqual({});
  });
});

describe("request() error envelope + auth header (P0)", () => {
  it("prefers .message, then .error, then statusText", async () => {
    server.use(
      http.get("/api/v1/users/current/stats", () =>
        HttpResponse.json({ message: "boom", error: "ignored" }, { status: 500 }),
      ),
    );
    await expect(
      api.getStats({ start: "a", end: "b" }),
    ).rejects.toMatchObject({ message: "boom", status: 500 });
  });

  it("falls back to .error when no .message", async () => {
    server.use(
      http.get("/api/v1/users/current/stats", () =>
        HttpResponse.json({ error: "nope" }, { status: 400 }),
      ),
    );
    await expect(api.getStats({ start: "a", end: "b" })).rejects.toMatchObject({
      message: "nope",
      status: 400,
    });
  });

  it("throws ApiError carrying status + payload", async () => {
    server.use(
      http.get("/api/v1/users/current/stats", () =>
        HttpResponse.json({ error: "e" }, { status: 403 }),
      ),
    );
    const err = await api.getStats({ start: "a", end: "b" }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(403);
    expect(err.payload).toEqual({ error: "e" });
  });

  it("sends Authorization only when a token is present, verbatim", async () => {
    let seen: string | null = "unset";
    server.use(
      http.get("/api/v1/users/current/stats", ({ request }) => {
        seen = request.headers.get("authorization");
        return HttpResponse.json({});
      }),
    );

    // No token -> no header.
    await api.getStats({ start: "a", end: "b" });
    expect(seen).toBeNull();

    // With token -> `Basic <token>` verbatim.
    authStore.update(authResponse({ token: "TOK123" }));
    await api.getStats({ start: "a", end: "b" });
    expect(seen).toBe("Basic TOK123");
  });
});

describe("api mutation call shapes (P1)", () => {
  it("addCurationRule POSTs the body to /curation", async () => {
    let body: unknown;
    server.use(
      http.post("/api/v1/users/current/curation", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ rule: { id: 1 } });
      }),
    );
    await api.addCurationRule({
      axis: "project",
      action: "rename",
      matchValue: "a",
      newValue: "b",
      matchType: "regex",
    });
    expect(body).toEqual({
      axis: "project",
      action: "rename",
      matchValue: "a",
      newValue: "b",
      matchType: "regex",
    });
  });

  it("deleteToken encodeURIComponent's the path param", async () => {
    let path = "";
    server.use(
      http.delete("/auth/token/:id", ({ params }) => {
        path = String(params.id);
        return new HttpResponse(null, { status: 200 });
      }),
    );
    await api.deleteToken("a/b c");
    expect(path).toBe("a/b c"); // msw decodes; the point is it didn't error
  });
});
