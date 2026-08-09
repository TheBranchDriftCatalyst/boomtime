import type { ReactNode } from "react";
import { createElement } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PublicConfig } from "@/types/api";

// usePublicConfig is mocked so we can drive the server-side kill switch
// deterministically without a react-query round-trip. betaRegistration's own
// logic (URL capture -> sessionStorage -> active gate) is what's under test.
let mockConfig: PublicConfig;
vi.mock("@/lib/usePublicConfig", () => ({
  usePublicConfig: () => ({ config: mockConfig, isLoading: false }),
}));

import {
  activateBeta,
  exitBeta,
  useBetaRegistration,
  useBetaStored,
} from "@/features/onboarding/betaRegistration";

const ALLOW: PublicConfig = {
  registration_enabled: true,
  auth_provider: "local",
  oidc_enabled: false,
  billing_enabled: false,
  beta_flags: { user_registration: true },
  github_connect_enabled: false,
  widget_spec_engine: false,
};

function wrapper(initialEntries: string[] = ["/"]) {
  return ({ children }: { children: ReactNode }) =>
    createElement(MemoryRouter, { initialEntries }, children);
}

describe("betaRegistration", () => {
  beforeEach(() => {
    sessionStorage.clear();
    mockConfig = { ...ALLOW, beta_flags: { ...ALLOW.beta_flags } };
  });
  afterEach(() => {
    sessionStorage.clear();
  });

  describe("store", () => {
    it("useBetaStored reflects activate/exit", () => {
      const { result } = renderHook(() => useBetaStored());
      expect(result.current).toBe(false);
      act(() => activateBeta());
      expect(result.current).toBe(true);
      act(() => exitBeta());
      expect(result.current).toBe(false);
    });
  });

  describe("useBetaRegistration", () => {
    it("is inactive with no URL param and nothing stored", () => {
      const { result } = renderHook(() => useBetaRegistration(), {
        wrapper: wrapper(["/app"]),
      });
      expect(result.current.active).toBe(false);
    });

    it("activates when ?enable_beta_user_registration=true is present on any path", async () => {
      const { result } = renderHook(() => useBetaRegistration(), {
        wrapper: wrapper(["/app/settings?enable_beta_user_registration=true"]),
      });
      await waitFor(() => expect(result.current.active).toBe(true));
      // and it persisted to sessionStorage (survives nav)
      expect(sessionStorage.getItem("boomtime:beta:user_registration")).toBe("1");
    });

    it("exit() deactivates and clears storage", async () => {
      const { result } = renderHook(() => useBetaRegistration(), {
        wrapper: wrapper(["/?enable_beta_user_registration=true"]),
      });
      await waitFor(() => expect(result.current.active).toBe(true));
      act(() => result.current.exit());
      expect(result.current.active).toBe(false);
      expect(sessionStorage.getItem("boomtime:beta:user_registration")).toBeNull();
    });

    it("?enable_beta_user_registration=false exits an active preview", async () => {
      activateBeta();
      const { result } = renderHook(() => useBetaRegistration(), {
        wrapper: wrapper(["/?enable_beta_user_registration=false"]),
      });
      await waitFor(() => expect(result.current.active).toBe(false));
    });

    it("stays inactive when the server kill switch disables it, even if stored", () => {
      activateBeta();
      mockConfig.beta_flags = { user_registration: false };
      const { result } = renderHook(() => useBetaRegistration(), {
        wrapper: wrapper(["/app"]),
      });
      expect(useBetaStoredValue()).toBe(true); // still stored
      expect(result.current.active).toBe(false); // but gated off by server
    });
  });
});

// Small helper to read the raw stored flag without a hook render context.
function useBetaStoredValue(): boolean {
  return sessionStorage.getItem("boomtime:beta:user_registration") === "1";
}
