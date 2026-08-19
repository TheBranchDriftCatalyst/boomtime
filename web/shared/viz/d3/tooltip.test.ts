import { describe, expect, it, beforeEach } from "vitest";
import * as d3 from "d3";
import {
  createTooltip,
  escapeHtml,
  hideTooltip,
  showTooltip,
  tooltipHtml,
} from "./tooltip";

describe("escapeHtml", () => {
  it("escapes all five HTML entities", () => {
    expect(escapeHtml(`&<>"'`)).toBe("&amp;&lt;&gt;&quot;&#39;");
  });

  it("escapes the ampersand FIRST so later entities aren't double-escaped", () => {
    // If "&" were replaced after "<", the "&lt;" produced for "<" would itself
    // get its ampersand re-escaped into "&amp;lt;". Pin the correct ordering:
    // a literal "&lt;" in the input comes out as "&amp;lt;" (shows as "&lt;"),
    // and a "<" comes out as exactly "&lt;" (shows as "<").
    expect(escapeHtml("&lt;")).toBe("&amp;lt;");
    expect(escapeHtml("<")).toBe("&lt;");
    expect(escapeHtml("a && b < c")).toBe("a &amp;&amp; b &lt; c");
  });

  it("passes through text without special characters unchanged", () => {
    expect(escapeHtml("plain text 123 café")).toBe("plain text 123 café");
  });
});

describe("tooltipHtml (legacy positional signature)", () => {
  it("renders the title in a bold div", () => {
    expect(tooltipHtml("My Title")).toBe(
      '<div style="font-weight:600">My Title</div>',
    );
  });

  it("renders plain string rows joined with <br/>", () => {
    expect(tooltipHtml("T", "row one", "row two")).toBe(
      '<div style="font-weight:600">T</div>row one<br/>row two',
    );
  });

  it("renders [label, value] rows as 'label: value'", () => {
    expect(tooltipHtml("T", ["Time", "1h 5m"])).toBe(
      '<div style="font-weight:600">T</div>Time: 1h 5m',
    );
  });

  it("mixes string rows and pair rows in order", () => {
    expect(tooltipHtml("T", ["Time", "2h"], "extra note")).toBe(
      '<div style="font-weight:600">T</div>Time: 2h<br/>extra note',
    );
  });

  it("neutralizes an XSS payload in the title, string rows, and pair rows", () => {
    const payload = '<img src=x onerror="alert(1)">';
    const html = tooltipHtml(payload, payload, [payload, payload]);
    expect(html).not.toContain("<img");
    expect(html).not.toContain('onerror="');
    expect(html).toContain(
      "&lt;img src=x onerror=&quot;alert(1)&quot;&gt;",
    );
  });

  // Extra XSS coverage for common attacker-controlled project names:
  it("neutralizes a <script> project name", () => {
    const html = tooltipHtml("<script>alert(1)</script>", "safe row");
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });

  it("neutralizes a project name with mixed &<> characters", () => {
    const html = tooltipHtml("A&B<C>D", ["A&B", "1<2"]);
    expect(html).toContain("A&amp;B&lt;C&gt;D");
    expect(html).toContain("A&amp;B");
    expect(html).toContain("1&lt;2");
    expect(html).not.toContain("A&B<C>D");
  });
});

describe("tooltipHtml (structured spec)", () => {
  it("dispatches to the spec-based renderer when given an object", () => {
    const out = tooltipHtml({
      title: "My Project",
      subtitle: "12–18 Jan 2026",
      rows: [{ label: "Time", value: "1h 5m" }],
      footer: "#3 of 14",
    });
    expect(out).toContain("My Project");
    expect(out).toContain("12–18 Jan 2026");
    expect(out).toContain("Time");
    expect(out).toContain("1h 5m");
    expect(out).toContain("#3 of 14");
  });

  it("escapes user-controlled names in a spec", () => {
    const evil = "<script>alert(1)</script>";
    const out = tooltipHtml({
      title: evil,
      subtitle: evil,
      rows: [{ label: evil, value: evil, swatch: "#ff0000" }],
    });
    expect(out).not.toContain("<script>");
    expect(out).toContain("&lt;script&gt;");
  });
});

// --- showTooltip (positioning + clamping) -----------------------------------

describe("showTooltip clamping", () => {
  let container: HTMLDivElement;

  beforeEach(() => {
    container = document.createElement("div");
    // Give the container a known viewport so the clamp math is deterministic.
    Object.defineProperty(container, "clientWidth", { configurable: true, value: 400 });
    Object.defineProperty(container, "clientHeight", { configurable: true, value: 300 });
    // Position the container at (0,0) so d3.pointer maps clientX/Y -> local.
    container.getBoundingClientRect = () =>
      ({ left: 0, top: 0, right: 400, bottom: 300, width: 400, height: 300, x: 0, y: 0, toJSON: () => ({}) } as DOMRect);
    document.body.appendChild(container);
  });

  function forceTooltipSize(tip: ReturnType<typeof createTooltip>, w: number, h: number) {
    const node = tip.node() as HTMLDivElement;
    // jsdom doesn't lay out; force offsetWidth/Height so clamp math has real values.
    Object.defineProperty(node, "offsetWidth", { configurable: true, value: w });
    Object.defineProperty(node, "offsetHeight", { configurable: true, value: h });
  }

  it("positions below-right of the cursor when there is room", () => {
    const tip = createTooltip(container);
    forceTooltipSize(tip, 80, 40);
    showTooltip(tip, container, { clientX: 100, clientY: 100 }, "<div>hi</div>");
    // pointer=(100,100), tip=80x40, GAP=12 => left=112, top=112
    expect(tip.style("left")).toBe("112px");
    expect(tip.style("top")).toBe("112px");
  });

  it("flips left of the cursor when it would overflow the right", () => {
    const tip = createTooltip(container);
    forceTooltipSize(tip, 200, 40);
    // pointer x=350: 350+12+200=562 > 400 -> flip: x=350-12-200=138
    showTooltip(tip, container, { clientX: 350, clientY: 50 }, "hi");
    expect(tip.style("left")).toBe("138px");
  });

  it("flips above the cursor when it would overflow the bottom", () => {
    const tip = createTooltip(container);
    forceTooltipSize(tip, 80, 100);
    // pointer y=250: 250+12+100=362 > 300 -> flip: y=250-12-100=138
    showTooltip(tip, container, { clientX: 100, clientY: 250 }, "hi");
    expect(tip.style("top")).toBe("138px");
  });

  it("clamps into the container when flipping still doesn't fit", () => {
    const tip = createTooltip(container);
    // Tooltip WIDER than the container: 500 > 400. right-flip goes negative.
    forceTooltipSize(tip, 500, 40);
    showTooltip(tip, container, { clientX: 350, clientY: 50 }, "hi");
    // fallback clamp: max(0, 400-500) = 0
    expect(tip.style("left")).toBe("0px");
  });

  it("makes the tooltip visible (opacity 1)", () => {
    const tip = createTooltip(container);
    forceTooltipSize(tip, 80, 40);
    showTooltip(tip, container, { clientX: 100, clientY: 100 }, "hi");
    expect(tip.style("opacity")).toBe("1");
  });

  it("hideTooltip drops opacity back to 0", () => {
    const tip = createTooltip(container);
    forceTooltipSize(tip, 80, 40);
    showTooltip(tip, container, { clientX: 100, clientY: 100 }, "hi");
    hideTooltip(tip);
    expect(tip.style("opacity")).toBe("0");
  });
});

// Sanity: createTooltip appends into the container (so lifecycle cleanup works).
describe("createTooltip", () => {
  it("appends a .gk-d3-tooltip div into the container", () => {
    const container = document.createElement("div");
    document.body.appendChild(container);
    createTooltip(container);
    const found = d3.select(container).select(".gk-d3-tooltip").node();
    expect(found).not.toBeNull();
  });
});
