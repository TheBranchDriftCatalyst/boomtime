// queryApi.test.ts — the query-DSL client's transport contract.
//
// The point of these tests is that runQuery goes through the SAME request()
// helper as every api.* call: before boom-28vm it hand-rolled its own fetch, so
// an access token that expired while a dashboard was open surfaced as a hard
// 401 on every query-DSL widget while the rest of the page silently refreshed
// and recovered.
import { describe, expect, it } from "vitest";
import { ApiError } from "@shared/lib/api";
import { runQuery, type QuerySpec } from "@shared/lib/queryApi";
import { authStore } from "@shared/features/auth/auth";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import { authResponse } from "@shared/test/factories";

const SPEC: QuerySpec = { domain: "coding", measure: "seconds" };

describe("runQuery transport (boom-28vm)", () => {
  it("refreshes and retries ONCE on a 401 instead of failing the widget", async () => {
    authStore.update(authResponse({ token: "STALE" }));
    let calls = 0;
    server.use(
      http.post("/api/v1/query", () => {
        calls += 1;
        // First hit 401s (expired access token); after the refresh, succeed.
        return calls === 1
          ? new HttpResponse(null, { status: 401 })
          : HttpResponse.json({ kind: "scalar", scalar: 42 });
      }),
      http.post("/auth/refresh_token", () =>
        HttpResponse.json(authResponse({ token: "FRESH" })),
      ),
    );
    await expect(runQuery(SPEC)).resolves.toEqual({ kind: "scalar", scalar: 42 });
    expect(calls).toBe(2); // 401 → refresh → retried once
    expect(authStore.isLoggedIn()).toBe(true);
  });

  it("sends the spec as the JSON body with the Authorization header", async () => {
    authStore.update(authResponse({ token: "TOK" }));
    let body: unknown;
    let authHeader: string | null = null;
    server.use(
      http.post("/api/v1/query", async ({ request }) => {
        body = await request.json();
        authHeader = request.headers.get("Authorization");
        return HttpResponse.json({ kind: "groups", groups: [{ key: "a", value: 1 }] });
      }),
    );
    await expect(runQuery(SPEC)).resolves.toEqual({
      kind: "groups",
      groups: [{ key: "a", value: 1 }],
    });
    expect(body).toEqual(SPEC);
    expect(authHeader).toBe(authStore.authHeader());
  });

  it("still throws ApiError with the server's message on a 400", async () => {
    server.use(
      http.post("/api/v1/query", () =>
        HttpResponse.json({ message: "unknown measure" }, { status: 400 }),
      ),
    );
    await expect(runQuery(SPEC)).rejects.toBeInstanceOf(ApiError);
    await expect(runQuery(SPEC)).rejects.toMatchObject({
      status: 400,
      message: "unknown measure",
    });
  });

  it("rejects a 200 text/html body (feature-gated-off SPA fallback)", async () => {
    server.use(
      http.post(
        "/api/v1/query",
        () =>
          new HttpResponse("<!doctype html><html></html>", {
            status: 200,
            headers: { "Content-Type": "text/html" },
          }),
      ),
    );
    await expect(runQuery(SPEC)).rejects.toBeInstanceOf(ApiError);
  });
});
