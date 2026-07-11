import { describe, expect, it } from "vitest";
import {
  escapeHtml,
  fmtDateRange,
  fmtDelta,
  fmtPct,
  fmtRank,
  otherBreakdownContent,
  tooltipHtml,
  type TooltipSpec,
} from "./tooltipContent";

describe("escapeHtml", () => {
  it("escapes all five HTML entities", () => {
    expect(escapeHtml(`&<>"'`)).toBe("&amp;&lt;&gt;&quot;&#39;");
  });

  it("escapes the ampersand first so later entities aren't double-escaped", () => {
    expect(escapeHtml("&lt;")).toBe("&amp;lt;");
    expect(escapeHtml("<")).toBe("&lt;");
  });

  it("passes through non-special text unchanged", () => {
    expect(escapeHtml("plain 123 café")).toBe("plain 123 café");
  });
});

describe("tooltipHtml (structured spec)", () => {
  it("renders the title inside a bold div", () => {
    expect(tooltipHtml({ title: "boomtime" })).toContain(
      '<div style="font-weight:600">boomtime</div>',
    );
  });

  it("renders a subtitle in a muted line", () => {
    const out = tooltipHtml({ title: "T", subtitle: "12–18 Jan 2026" });
    expect(out).toContain("12–18 Jan 2026");
    expect(out).toContain("opacity:0.7");
  });

  it("renders rows as label/value pairs", () => {
    const out = tooltipHtml({
      title: "T",
      rows: [
        { label: "Time", value: "1h 5m" },
        { label: "Share", value: "42.0%" },
      ],
    });
    expect(out).toContain("Time");
    expect(out).toContain("1h 5m");
    expect(out).toContain("Share");
    expect(out).toContain("42.0%");
  });

  it("renders the footer in a muted line", () => {
    const out = tooltipHtml({ title: "T", footer: "#3 of 14" });
    expect(out).toContain("#3 of 14");
  });

  it("renders a title swatch when titleSwatch is set", () => {
    const out = tooltipHtml({ title: "T", titleSwatch: "#ff00aa" });
    expect(out).toContain("#ff00aa");
    expect(out).toMatch(/background:#ff00aa/);
  });

  it("renders row swatches when provided", () => {
    const out = tooltipHtml({
      title: "T",
      rows: [{ label: "coding", value: "1h", swatch: "#123456" }],
    });
    expect(out).toContain("#123456");
  });

  // --- XSS coverage -------------------------------------------------------

  it("neutralizes a <script> payload injected via project name in the title", () => {
    const spec: TooltipSpec = { title: "<script>alert(1)</script>" };
    const out = tooltipHtml(spec);
    expect(out).not.toContain("<script>");
    expect(out).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
  });

  it("neutralizes a <script> payload injected via project name in a row label AND value", () => {
    const payload = "<script>alert('xss')</script>";
    const out = tooltipHtml({
      title: "safe",
      rows: [{ label: payload, value: payload }],
    });
    expect(out).not.toContain("<script>");
    expect(out).not.toContain("onerror=");
    expect(out).toContain("&lt;script&gt;");
  });

  it("escapes &<> characters in a project name (subtitle + rows)", () => {
    const evil = "a&b<c>d";
    const out = tooltipHtml({
      title: evil,
      subtitle: evil,
      rows: [{ label: evil, value: evil }],
      footer: "safe-footer",
    });
    // Title and subtitle should be escaped
    expect(out).toContain("a&amp;b&lt;c&gt;d");
    expect(out).not.toContain("a&b<c>d");
    // The literal < with content should not survive
    expect(out).not.toMatch(/[^&]<c>/);
  });

  it("escapes an <img onerror> payload injected via a title swatch value", () => {
    // If a caller ever passed an attacker-controlled value as swatch, we
    // still escape it before dropping into style.
    const out = tooltipHtml({
      title: "safe",
      titleSwatch: `red;background:url('x');/* <img onerror="alert(1)"> */`,
    });
    expect(out).not.toContain('<img');
    expect(out).not.toContain('onerror="alert(1)"');
  });
});

describe("fmtPct", () => {
  it("formats with one decimal", () => {
    expect(fmtPct(42.3456)).toBe("42.3%");
    expect(fmtPct(0)).toBe("0.0%");
    expect(fmtPct(100)).toBe("100.0%");
  });
  it("clamps to [0,100]", () => {
    expect(fmtPct(-5)).toBe("0.0%");
    expect(fmtPct(120)).toBe("100.0%");
  });
  it("handles NaN/Infinity gracefully", () => {
    expect(fmtPct(NaN)).toBe("0.0%");
    expect(fmtPct(Infinity)).toBe("0.0%");
  });
});

describe("fmtRank", () => {
  it('formats "#R of N" 1-indexed', () => {
    expect(fmtRank(3, 14)).toBe("#3 of 14");
    expect(fmtRank(1, 1)).toBe("#1 of 1");
  });
  it("returns empty on invalid input", () => {
    expect(fmtRank(0, 5)).toBe("");
    expect(fmtRank(3, 0)).toBe("");
    expect(fmtRank(-1, 5)).toBe("");
    expect(fmtRank(NaN, 5)).toBe("");
  });
});

describe("fmtDateRange", () => {
  it("collapses same-day range", () => {
    expect(fmtDateRange("2026-01-12T00:00:00Z", "2026-01-12T00:00:00Z")).toBe(
      "12 Jan 2026",
    );
  });
  it("compacts same month/year range", () => {
    expect(fmtDateRange("2026-01-12T00:00:00Z", "2026-01-18T00:00:00Z")).toBe(
      "12–18 Jan 2026",
    );
  });
  it("crosses month within same year", () => {
    expect(fmtDateRange("2026-01-28T00:00:00Z", "2026-02-03T00:00:00Z")).toBe(
      "28 Jan – 3 Feb 2026",
    );
  });
  it("crosses year", () => {
    expect(fmtDateRange("2025-12-28T00:00:00Z", "2026-01-03T00:00:00Z")).toBe(
      "28 Dec 2025 – 3 Jan 2026",
    );
  });
  it("returns empty on invalid ISO", () => {
    expect(fmtDateRange("nope", "2026-01-12T00:00:00Z")).toBe("");
    expect(fmtDateRange("2026-01-12", "nope")).toBe("");
  });
});

describe("fmtDelta", () => {
  it("returns empty when both are zero", () => {
    expect(fmtDelta(0, 0)).toBe("");
  });
  it("marks up positive deltas with an up arrow and % of prev", () => {
    // 3600 -> 5400 = +50%
    const out = fmtDelta(5400, 3600);
    expect(out).toContain("▲");
    expect(out).toContain("+50%");
    expect(out).toContain("var(--success");
  });
  it("marks up negative deltas with a down arrow and negative %", () => {
    // 3600 -> 1800 = -50%
    const out = fmtDelta(1800, 3600);
    expect(out).toContain("▼");
    expect(out).toContain("-50%");
    expect(out).toContain("var(--destructive");
  });
  it("shows '(new)' when prev is 0 but cur > 0", () => {
    const out = fmtDelta(3600, 0);
    expect(out).toContain("▲");
    expect(out).toContain("(new)");
  });
  it("shows a compact duration in the delta string", () => {
    // Diff of exactly 3600s => "1h"
    const out = fmtDelta(7200, 3600);
    expect(out).toContain("1h");
  });
  it("shows 'no change' when diff is zero but activity exists", () => {
    expect(fmtDelta(3600, 3600)).toContain("no change");
  });
});

describe("otherBreakdownContent (gaka-7m4)", () => {
  const hms = (s: number) => `${s}s`;

  it("returns one row per member with a time + intra-Other share", () => {
    const { rows } = otherBreakdownContent(
      {
        otherMembers: [
          { name: "python", totalSeconds: 300, totalPct: 3 },
          { name: "ruby", totalSeconds: 100, totalPct: 1 },
        ],
        otherCount: 2,
      },
      hms,
    );
    expect(rows).toHaveLength(2);
    expect(rows[0].label).toBe("python");
    expect(rows[0].value).toBe("300s (75.0%)");
    expect(rows[1].label).toBe("ruby");
    expect(rows[1].value).toBe("100s (25.0%)");
  });

  it("uses caller-provided formatTime for the duration part", () => {
    const iso = (s: number) => `${Math.floor(s / 60)}m`;
    const { rows } = otherBreakdownContent(
      { otherMembers: [{ name: "go", totalSeconds: 180, totalPct: 0 }], otherCount: 1 },
      iso,
    );
    expect(rows[0].value.startsWith("3m")).toBe(true);
  });

  it("attaches swatches when a swatchAt is provided", () => {
    const { rows } = otherBreakdownContent(
      {
        otherMembers: [
          { name: "a", totalSeconds: 1, totalPct: 0 },
          { name: "b", totalSeconds: 1, totalPct: 0 },
        ],
        otherCount: 2,
      },
      hms,
      (i) => (i === 0 ? "#f0f" : "#0ff"),
    );
    expect(rows[0].swatch).toBe("#f0f");
    expect(rows[1].swatch).toBe("#0ff");
  });

  it("footer notes overflow when OtherCount > len(members)", () => {
    const { footer } = otherBreakdownContent(
      { otherMembers: [{ name: "a", totalSeconds: 1, totalPct: 0 }], otherCount: 5 },
      hms,
    );
    expect(footer).toBe("+4 more not shown");
  });

  it("footer is empty when OtherCount matches len(members)", () => {
    const { footer } = otherBreakdownContent(
      { otherMembers: [{ name: "a", totalSeconds: 1, totalPct: 0 }], otherCount: 1 },
      hms,
    );
    expect(footer).toBe("");
  });

  it("empty members yield empty rows and no overflow footer", () => {
    const { rows, footer } = otherBreakdownContent(
      { otherMembers: [], otherCount: 0 },
      hms,
    );
    expect(rows).toHaveLength(0);
    expect(footer).toBe("");
  });

  it("survives a missing otherMembers field (safe on non-Other rows)", () => {
    const { rows, footer } = otherBreakdownContent({}, hms);
    expect(rows).toHaveLength(0);
    expect(footer).toBe("");
  });
});
