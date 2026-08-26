// LabsFlags.test.tsx (gaka-lzr) — the shared flag-row list Settings > Labs
// and the public-profile FlagsFlipper both render. Covers: every
// FEATURE_FLAGS entry shows up labeled + described, toggling flips the
// underlying localStorage-backed flag (featureFlags.ts), and flags stay
// default-off on a fresh render (push-safety).
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { LabsFlags } from "./LabsFlags";
import { FEATURE_FLAGS, getFlag } from "@shared/lib/featureFlags";

describe("LabsFlags", () => {
  it("renders one labeled + described row per FEATURE_FLAGS entry", () => {
    render(<LabsFlags />);
    for (const f of FEATURE_FLAGS) {
      const row = screen.getByTestId(`flag-${f.key}`);
      expect(row).toHaveTextContent(f.label);
      if (f.description) expect(row).toHaveTextContent(f.description);
    }
  });

  it("every flag starts OFF (default-off / push-safety)", () => {
    render(<LabsFlags />);
    for (const f of FEATURE_FLAGS) {
      expect(getFlag(f.key)).toBe(false);
      expect(screen.getByTestId(`flag-switch-${f.key}`)).toHaveAttribute(
        "aria-checked",
        "false",
      );
    }
  });

  it("toggling a switch flips the underlying flag", async () => {
    render(<LabsFlags />);
    const key = FEATURE_FLAGS[0].key;
    expect(getFlag(key)).toBe(false);

    await userEvent.click(screen.getByTestId(`flag-switch-${key}`));

    expect(getFlag(key)).toBe(true);
    expect(screen.getByTestId(`flag-switch-${key}`)).toHaveAttribute(
      "aria-checked",
      "true",
    );
  });

  it("the compact 'menu' variant renders the same rows (FlagsFlipper reuse)", () => {
    render(<LabsFlags variant="menu" />);
    for (const f of FEATURE_FLAGS) {
      expect(screen.getByTestId(`flag-${f.key}`)).toHaveTextContent(f.label);
    }
  });
});
