// AppShellNoScroll — structural guard for the grid track floors (gaka-c26s).
//
// WHY A CLASS ASSERTION AND NOT A LAYOUT ASSERTION: jsdom does not implement
// CSS grid, so every element here measures 0x0 and a width-based test would
// pass no matter what the template says. The real behavioral check lives in
// web/e2e/shell-layout.spec.ts, which drives a browser and asserts the header
// stays inside the viewport at laptop widths. This file only pins the two
// tokens that test proved load-bearing, so a "tidy up the classes" refactor
// can't quietly drop one and reintroduce a bug that is invisible in unit tests.
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AppShellNoScroll } from "./AppShellNoScroll";

describe("AppShellNoScroll", () => {
  function shellRoot() {
    const { container } = render(
      <AppShellNoScroll sidebar={<nav />} header={<header />}>
        <main />
      </AppShellNoScroll>,
    );
    return container.firstElementChild as HTMLElement;
  }

  it("clamps BOTH grid track floors to 0", () => {
    const cls = shellRoot().className;

    // Columns: without this, a wide child (the admin/settings tab strip) makes
    // the content track wider than the viewport and overflow-hidden clips the
    // header's right-side controls out of reach entirely.
    expect(cls).toContain("grid-cols-[auto_minmax(0,1fr)]");

    // Rows: without this, the content cell refuses to shrink and Page.Content's
    // overflow-y-auto never engages — the original no-scroll-shell bug.
    expect(cls).toContain("grid-rows-[auto_minmax(0,1fr)]");
  });

  it("keeps the viewport-owning classes that make the shell no-scroll", () => {
    const cls = shellRoot().className;
    expect(cls).toContain("h-dvh");
    expect(cls).toContain("overflow-hidden");
  });
});
