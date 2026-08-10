import { describe, expect, it, vi } from "vitest";
import { openCommandPalette } from "@/components/CommandPalette";

// The header/mobile search buttons open the palette via openCommandPalette()
// rather than a shared ref/context — lock that decoupled contract (the palette
// listens for this exact event; a rename here silently breaks every trigger).
describe("openCommandPalette", () => {
  it("dispatches the boomtime:open-command-palette window event once", () => {
    const spy = vi.fn();
    window.addEventListener("boomtime:open-command-palette", spy);
    openCommandPalette();
    openCommandPalette();
    expect(spy).toHaveBeenCalledTimes(2);
    window.removeEventListener("boomtime:open-command-palette", spy);
  });
});
