// Unit coverage for the STANDALONE books auth gate (boom-zp2s) — the FE mirror
// of the backend's auth.SetStandaloneOwner short-circuit.
//
// AuthProvider dispatches on the VITE_BOOKS_STANDALONE build constant (read at
// module load), so each test stubs the env and re-imports the module graph
// fresh. The load-bearing, non-tautological contrast:
//
//   - standalone (flag=true): the owner is ALWAYS logged in, with NO
//     /auth/refresh_token bootstrap — api.refreshToken is NEVER called and
//     bootstrapping is false from the first render.
//   - host (flag off): the SAME provider bootstraps the session — api.refreshToken
//     IS called. This is the assertion that fails if the standalone gate leaked
//     into the host build (a network call the lean books server never serves), or
//     if the host build stopped bootstrapping.
//
// api is mocked so the host path's bootstrap resolves locally (no msw network)
// and the call is observable.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

// Spy on refreshToken; keep every other real export (ApiError etc. are used by
// useAuth). It rejects so the host bootstrap settles to "not authenticated".
const refreshToken = vi.fn(() => Promise.reject(new Error("no session")));
vi.mock("@shared/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@shared/lib/api")>();
  return { ...actual, api: { ...actual.api, refreshToken } };
});

async function renderAuth() {
  const { AuthProvider, useAuth } = await import(
    "@shared/features/auth/useAuth"
  );
  function Probe() {
    const a = useAuth();
    return (
      <div>
        <span data-testid="logged">{String(a.isLoggedIn)}</span>
        <span data-testid="user">{a.username}</span>
        <span data-testid="boot">{String(a.bootstrapping)}</span>
      </div>
    );
  }
  render(
    <AuthProvider>
      <Probe />
    </AuthProvider>,
  );
}

describe("StandaloneAuthProvider (VITE_BOOKS_STANDALONE=true)", () => {
  beforeEach(() => {
    vi.resetModules();
    refreshToken.mockClear();
    vi.stubEnv("VITE_BOOKS_STANDALONE", "true");
  });
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("the owner is logged in immediately with NO bootstrap network call", async () => {
    await renderAuth();

    expect(screen.getByTestId("logged")).toHaveTextContent("true");
    expect(screen.getByTestId("user")).toHaveTextContent("owner");
    // bootstrapping is false from the first render — no async session probe.
    expect(screen.getByTestId("boot")).toHaveTextContent("false");

    // The single hardest assertion: the standalone provider must NEVER call the
    // refresh endpoint — the lean books server does not serve it.
    expect(refreshToken).not.toHaveBeenCalled();
  });

  it("honors VITE_BOOKS_OWNER for the standalone username", async () => {
    vi.stubEnv("VITE_BOOKS_OWNER", "librarian");
    await renderAuth();
    expect(screen.getByTestId("user")).toHaveTextContent("librarian");
    expect(refreshToken).not.toHaveBeenCalled();
  });
});

describe("host AuthProvider (flag off) — the contrast that proves the gate", () => {
  beforeEach(() => {
    vi.resetModules();
    refreshToken.mockClear();
    vi.stubEnv("VITE_BOOKS_STANDALONE", "false");
  });
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("bootstraps the session (api.refreshToken IS called) — proving standalone is a real divergence", async () => {
    await renderAuth();
    // The host provider probes the refresh cookie on mount; the standalone one
    // never does. If this stopped being true, the two paths would be identical
    // and the standalone gate meaningless.
    await waitFor(() => expect(refreshToken).toHaveBeenCalled());
  });
});
