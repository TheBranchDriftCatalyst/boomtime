// LabsTab.test.tsx (gaka-lzr) — Settings > Labs: renders the shared
// LabsFlags list plus the beta-registration preview row, and the latter
// respects the server kill switch (usePublicConfig's beta_flags.user_registration).
import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { LabsTab } from "./LabsTab";
import { FEATURE_FLAGS } from "@/lib/featureFlags";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/app/settings?tab=labs"]}>{children}</MemoryRouter>
    </QueryClientProvider>
  );
}

describe("LabsTab", () => {
  it("renders every FEATURE_FLAGS entry via LabsFlags", () => {
    render(<LabsTab />, { wrapper: wrapper() });
    for (const f of FEATURE_FLAGS) {
      expect(screen.getByTestId(`flag-${f.key}`)).toHaveTextContent(f.label);
    }
  });

  it("enables the beta-registration row when the server allows it (default)", () => {
    render(<LabsTab />, { wrapper: wrapper() });
    expect(screen.getByTestId("labs-beta-registration-switch")).toBeEnabled();
  });

  it("disables the beta-registration row when the server kill switch is off", async () => {
    server.use(
      http.get("/api/v1/config/public", () =>
        HttpResponse.json({
          registration_enabled: true,
          auth_provider: "local",
          oidc_enabled: false,
          billing_enabled: false,
          beta_flags: { user_registration: false },
          github_connect_enabled: false,
        }),
      ),
    );
    render(<LabsTab />, { wrapper: wrapper() });
    // The switch is present immediately (usePublicConfig's optimistic
    // FALLBACK allows by default) and only flips to disabled once the
    // overridden /api/v1/config/public response lands — wait for the
    // ATTRIBUTE, not just element presence.
    await waitFor(() =>
      expect(screen.getByTestId("labs-beta-registration-switch")).toBeDisabled(),
    );
  });
});
