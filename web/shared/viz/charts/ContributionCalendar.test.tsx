// ContributionCalendar.test.tsx (boom-csx P3 / boom-nmk) — the additive-invariant
// guard for the GitHub commit overlay.
//
// Invariant (A): when NO `ghValues` prop is supplied, the calendar renders
// BYTE-IDENTICAL to the coding-time-only calendar — no overlay DOM, no message.
// When `ghValues` IS supplied, the commit COUNT is drawn as a text label
// (`text.gh-count`) on each day with commits, layered over the unchanged base
// cells. (boom-nmk replaced the old low-contrast `path.gh-corner` triangle.)
//
// jsdom has no layout; ContributionCalendar draws with sizeToFrame:false so the
// D3 draw runs on mount regardless of a measured width — the cells + overlay are
// in the DOM after the effect flushes.
import { render, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ContributionCalendar } from "./ContributionCalendar";

const dates = ["2026-01-01", "2026-01-02", "2026-01-03", "2026-01-04"];
const values = [3600, 0, 7200, 1800]; // coding seconds/day
const gh = [10, 0, 20, 5]; // GitHub commits/day (3 non-zero days)

/** Serialize the svg with every overlay mark removed — the "base" structure. */
function baseStructure(svg: SVGSVGElement): string {
  const clone = svg.cloneNode(true) as SVGSVGElement;
  clone.querySelectorAll("text.gh-count").forEach((n) => n.remove());
  return clone.innerHTML;
}

describe("ContributionCalendar GitHub overlay (boom-csx P3)", () => {
  it("renders NO overlay elements and identical base structure when ghValues is absent", async () => {
    const { container } = render(
      <ContributionCalendar dates={dates} values={values} />,
    );
    await waitFor(() =>
      expect(container.querySelectorAll("g.day").length).toBe(dates.length),
    );
    // The invariant: zero overlay DOM when the prop is absent.
    expect(container.querySelectorAll("text.gh-count").length).toBe(0);
    // Base cells: a floor rect + a primary rect per day.
    expect(container.querySelectorAll("g.day rect").length).toBe(
      dates.length * 2,
    );
  });

  it("adds a gh-count label per commit-day WITHOUT changing the base cell structure", async () => {
    const withoutGh = render(
      <ContributionCalendar dates={dates} values={values} />,
    );
    await waitFor(() =>
      expect(
        withoutGh.container.querySelectorAll("g.day").length,
      ).toBe(dates.length),
    );
    const baseSvg = withoutGh.container.querySelector("svg")!;
    const baseHtml = baseStructure(baseSvg);
    withoutGh.unmount();

    const withGh = render(
      <ContributionCalendar dates={dates} values={values} ghValues={gh} />,
    );
    await waitFor(() =>
      expect(
        withGh.container.querySelectorAll("text.gh-count").length,
      ).toBeGreaterThan(0),
    );
    const overlaySvg = withGh.container.querySelector("svg")!;

    // One label per day that had commits (3 of 4).
    const labels = overlaySvg.querySelectorAll("text.gh-count");
    expect(labels.length).toBe(3);
    // Each label renders the actual commit COUNT for that day, in order.
    expect([...labels].map((n) => n.textContent)).toEqual(["10", "20", "5"]);
    // Byte-identical base: stripping the overlay yields the exact same DOM as
    // the no-overlay render (the overlay is purely additive).
    expect(baseStructure(overlaySvg)).toBe(baseHtml);
  });
});
