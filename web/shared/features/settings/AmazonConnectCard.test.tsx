// AmazonConnectCard.test.tsx — the Kindle trigger row (shared Amazon device
// feeds BOTH Audible + Kindle). Non-tautological coverage:
//   1. The Kindle row only renders once Amazon is connected.
//   2. "Backfill Kindle library" POSTs /api/v1/kindle/backfill and toasts the jobId.
//   3. "Sync Kindle" POSTs /api/v1/kindle/sync and toasts the synced count.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
  },
}));

import { AmazonConnectCard } from "@shared/features/settings/AmazonConnectCard";
import { authStore } from "@shared/features/auth/auth";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";

function enableBooks() {
  server.use(
    http.get("/api/v1/config/public", () =>
      HttpResponse.json({
        registration_enabled: true,
        auth_provider: "local",
        oidc_enabled: false,
        billing_enabled: false,
        beta_flags: {},
        github_connect_enabled: false,
        books_enabled: true,
      }),
    ),
  );
}

function amazon(connected: boolean) {
  server.use(
    http.get("/api/v1/amazon", () => HttpResponse.json({ connected })),
    http.get("/api/v1/books/items", () => HttpResponse.json({ items: [] })),
  );
}

beforeEach(() => {
  authStore.update({
    token: "test-token",
    tokenExpiry: new Date(Date.now() + 60_000).toISOString(),
    tokenUsername: "panda",
  });
  toastSuccess.mockClear();
  toastError.mockClear();
});

afterEach(() => {
  authStore.clear();
});

describe("AmazonConnectCard — Kindle triggers", () => {
  it("does not render the Kindle row until Amazon is connected", async () => {
    enableBooks();
    amazon(false);
    renderWithProviders(<AmazonConnectCard />, { withRouter: true });

    await waitFor(() =>
      expect(screen.getByRole("button", { name: /connect amazon/i })).toBeInTheDocument(),
    );
    expect(screen.queryByRole("button", { name: /backfill kindle library/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /sync kindle/i })).not.toBeInTheDocument();
  });

  it("backfills Kindle → POST /kindle/backfill + toast with jobId", async () => {
    enableBooks();
    amazon(true);
    let hit = 0;
    server.use(
      http.post("/api/v1/kindle/backfill", () => {
        hit += 1;
        return HttpResponse.json({ enqueued: true, jobId: 77 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<AmazonConnectCard />, { withRouter: true });

    const btn = await screen.findByRole("button", { name: /backfill kindle library/i });
    await user.click(btn);

    await waitFor(() => expect(hit).toBe(1));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Kindle backfill started (job #77)"),
    );
  });

  it("syncs Kindle → POST /kindle/sync + toast with count", async () => {
    enableBooks();
    amazon(true);
    let hit = 0;
    server.use(
      http.post("/api/v1/kindle/sync", () => {
        hit += 1;
        return HttpResponse.json({ synced: 3, source: "kindle" });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<AmazonConnectCard />, { withRouter: true });

    const btn = await screen.findByRole("button", { name: /sync kindle/i });
    await user.click(btn);

    await waitFor(() => expect(hit).toBe(1));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Synced 3 Kindle items"));
  });

  it("surfaces the ApiError message as an error toast on failure", async () => {
    enableBooks();
    amazon(true);
    server.use(
      http.post("/api/v1/kindle/backfill", () =>
        HttpResponse.json({ message: "device offline" }, { status: 502 }),
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<AmazonConnectCard />, { withRouter: true });

    await user.click(await screen.findByRole("button", { name: /backfill kindle library/i }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("device offline"));
    expect(toastSuccess).not.toHaveBeenCalled();
  });
});

// --- boom-28vm: the bookmarklet return path ---------------------------------
// The bookmarklet navigates back to
//   /app/settings?tab=connections&amazonCaptured=<encodeURIComponent(maplandingUrl)>
// URLSearchParams.get() decodes that ONCE. The card used to decode it a SECOND
// time, chewing through the escapes inside the Amazon URL's own query values:
// %26 became a live "&" (splitting the query), %23 became "#" (truncating
// everything after it — including openid.oa2.authorization_code), and a stray
// "%" made decodeURIComponent throw URIError inside the effect.

const SESSION_KEY = "boomtime.amazon.session";

// A realistic maplanding URL whose openid params carry percent-encoded URLs.
const MAPLANDING =
  "https://www.amazon.com/ap/maplanding?openid.oa2.authorization_code=ANxyz123" +
  "&openid.return_to=https%3A%2F%2Fwww.amazon.com%2Fap%2Fmaplanding%3Fa%3D1%26b%3D2" +
  "&openid.identity=https%3A%2F%2Fwww.amazon.com%2Fap%2Fid%2Famzn1.account.ABC%23frag" +
  "&openid.mode=id_res";

// A plain MemoryRouter (not renderWithProviders' data router): the capture
// effect finishes by rewriting the query string, and a data-router navigation
// under msw's interceptor blows up in jsdom for reasons unrelated to this test.
function renderAtCapture(captured: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter
        initialEntries={[
          "/app/settings?tab=connections&amazonCaptured=" + encodeURIComponent(captured),
        ]}
      >
        <AmazonConnectCard />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("AmazonConnectCard — bookmarklet capture", () => {
  it("posts the captured maplanding URL VERBATIM (no second decode)", async () => {
    enableBooks();
    amazon(false);
    let body: { session?: string; redirectUrl?: string } | null = null;
    server.use(
      http.post("/api/v1/amazon/connect/complete", async ({ request }) => {
        body = (await request.json()) as { session?: string; redirectUrl?: string };
        return HttpResponse.json({ connected: true });
      }),
    );
    localStorage.setItem(SESSION_KEY, "SESSION-TOKEN");

    renderAtCapture(MAPLANDING);

    await waitFor(() => expect(body).not.toBeNull());
    expect(body!.session).toBe("SESSION-TOKEN");
    // Pre-fix this arrived with %3A%2F%2F decoded to "://", %26 to "&" and %23
    // to "#" — a different, broken URL the server could not parse a code out of.
    expect(body!.redirectUrl).toBe(MAPLANDING);
    expect(body!.redirectUrl).toContain("%23frag");
    localStorage.removeItem(SESSION_KEY);
  });

  it("does not crash when the captured URL contains a bare '%'", async () => {
    enableBooks();
    amazon(false);
    let body: { redirectUrl?: string } | null = null;
    server.use(
      http.post("/api/v1/amazon/connect/complete", async ({ request }) => {
        body = (await request.json()) as { redirectUrl?: string };
        return HttpResponse.json({ connected: true });
      }),
    );
    localStorage.setItem(SESSION_KEY, "SESSION-TOKEN");
    // decodeURIComponent("...AN100%off") throws URIError — pre-fix that blew up
    // inside the effect and took the route to the error boundary.
    const withBarePercent =
      "https://www.amazon.com/ap/maplanding?openid.oa2.authorization_code=AN100%off";

    renderAtCapture(withBarePercent);

    await waitFor(() => expect(body).not.toBeNull());
    expect(body!.redirectUrl).toBe(withBarePercent);
    localStorage.removeItem(SESSION_KEY);
  });
});
