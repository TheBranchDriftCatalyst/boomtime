// Unit guard for the STANDALONE books public-config gate (boom-zp2s / boom-omt2).
//
// The lean catalyst-books server serves NO /api/v1/config/public endpoint, so the
// host fetch would 404 → FALLBACK books_enabled:false → the Books nav is hidden
// and the page shows "Books isn't enabled". usePublicConfig reads the
// VITE_BOOKS_STANDALONE build constant (at module load) and must instead report a
// fixed standalone config with books ON and skip the doomed request.
//
// Non-tautological contrast (each side fails on a real regression):
//   - standalone (flag=true): books_enabled=true AND api.publicConfig NEVER called.
//     Fails if the fix regresses (→ Books hidden) or if it starts hitting the
//     nonexistent endpoint.
//   - host (flag off): api.publicConfig IS called and the server's value wins
//     (here false). Fails if the standalone gate leaked into the host build and
//     force-enabled books regardless of the server.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const publicConfig = vi.fn(() =>
  Promise.resolve({
    registration_enabled: true,
    auth_provider: "local",
    oidc_enabled: false,
    billing_enabled: false,
    beta_flags: {},
    github_connect_enabled: false,
    books_enabled: false, // server says OFF — only the standalone gate flips it on
  }),
);
vi.mock("@shared/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@shared/lib/api")>();
  return { ...actual, api: { ...actual.api, publicConfig } };
});

function Wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

async function renderConfig() {
  const { usePublicConfig } = await import("@shared/lib/usePublicConfig");
  return renderHook(() => usePublicConfig(), { wrapper: Wrapper });
}

beforeEach(() => {
  vi.resetModules();
  publicConfig.mockClear();
});
afterEach(() => {
  vi.unstubAllEnvs();
});

describe("usePublicConfig standalone gate (boom-zp2s)", () => {
  it("standalone: books_enabled=true and NEVER fetches the missing /config/public", async () => {
    vi.stubEnv("VITE_BOOKS_STANDALONE", "true");
    const { result } = await renderConfig();
    expect(result.current.config.books_enabled).toBe(true);
    expect(publicConfig).not.toHaveBeenCalled();
  });

  it("host: fetches config + honors the server's books_enabled (no forcing)", async () => {
    const { result } = await renderConfig();
    await waitFor(() => expect(publicConfig).toHaveBeenCalled());
    await waitFor(() => expect(result.current.config.books_enabled).toBe(false));
  });
});
