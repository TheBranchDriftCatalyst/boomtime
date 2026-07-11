import { describe, expect, it } from "vitest";
import { authStore } from "@/features/auth/auth";
import { authResponse } from "@/test/factories";

// authStore is reset between tests by src/test/setup.ts (authStore.clear()).

describe("authStore.authHeader (P0 — the old 403 double-encode bug)", () => {
  it("returns `Basic <token>` verbatim, NOT re-encoded", () => {
    // The backend already returns the access token as base64(uuid). It must be
    // sent AS-IS; re-encoding would double-base64 it and the server 403s.
    const token = "YWJjZDEyMzQ="; // base64("abcd1234")
    authStore.update(authResponse({ token }));
    expect(authStore.authHeader()).toBe(`Basic ${token}`);
    // Guard against a regression that base64-encodes the token again.
    expect(authStore.authHeader()).not.toBe(`Basic ${btoa(token)}`);
  });

  it("returns null when there is no token", () => {
    expect(authStore.authHeader()).toBeNull();
  });
});

describe("authStore state", () => {
  it("update maps AuthResponse -> snapshot fields", () => {
    authStore.update(
      authResponse({
        token: "t1",
        tokenExpiry: "2026-07-10T12:00:00.000Z",
        tokenUsername: "panda",
      }),
    );
    const s = authStore.getSnapshot();
    expect(s.token).toBe("t1");
    expect(s.tokenExpiry).toBe("2026-07-10T12:00:00.000Z");
    expect(s.username).toBe("panda");
    expect(authStore.getUsername()).toBe("panda");
  });

  it("isLoggedIn requires BOTH token and expiry", () => {
    expect(authStore.isLoggedIn()).toBe(false);
    authStore.update(authResponse({ token: "t", tokenExpiry: "" }));
    expect(authStore.isLoggedIn()).toBe(false);
    authStore.update(authResponse({ token: "", tokenExpiry: "2026-01-01" }));
    expect(authStore.isLoggedIn()).toBe(false);
    authStore.update(authResponse({ token: "t", tokenExpiry: "2026-01-01" }));
    expect(authStore.isLoggedIn()).toBe(true);
  });

  it("tokenExpiry() parses the ISO string to epoch ms", () => {
    authStore.update(authResponse({ tokenExpiry: "2026-07-10T12:00:00.000Z" }));
    expect(authStore.tokenExpiry()).toBe(
      new Date("2026-07-10T12:00:00.000Z").getTime(),
    );
  });

  it("clear resets and notifies subscribers", () => {
    let calls = 0;
    const unsub = authStore.subscribe(() => calls++);
    authStore.update(authResponse());
    authStore.clear();
    expect(authStore.isLoggedIn()).toBe(false);
    expect(calls).toBeGreaterThanOrEqual(2);
    unsub();
  });
});
