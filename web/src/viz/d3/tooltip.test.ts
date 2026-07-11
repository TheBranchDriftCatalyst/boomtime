import { describe, expect, it } from "vitest";
import { escapeHtml, tooltipHtml } from "./tooltip";

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

describe("tooltipHtml", () => {
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
});
