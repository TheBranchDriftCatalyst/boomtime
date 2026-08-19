import { describe, expect, it } from "vitest";
import {
  customWidgetUrl,
  encodeDef,
  panelCount,
  type WidgetDef,
} from "./builder";

describe("builder encode/URL", () => {
  it("panelCount matches the layout catalog", () => {
    expect(panelCount("1-panel")).toBe(1);
    expect(panelCount("2-panel-h")).toBe(2);
    expect(panelCount("2-panel-v")).toBe(2);
    expect(panelCount("3-panel-h")).toBe(3);
  });

  it("encodeDef is url-safe base64 (no +, /, or =)", () => {
    const def: WidgetDef = {
      layout: "3-panel-h",
      title: "profile",
      panels: [{ kind: "calendar" }, { kind: "top-langs" }, { kind: "grade" }],
    };
    const enc = encodeDef(def);
    expect(enc).not.toMatch(/[+/=]/);
    // Round-trip via same substitution logic used server-side.
    const std = enc.replace(/-/g, "+").replace(/_/g, "/");
    const pad = std.length % 4 ? "=".repeat(4 - (std.length % 4)) : "";
    const decoded = JSON.parse(atob(std + pad));
    expect(decoded.layout).toBe("3-panel-h");
    expect(decoded.panels).toHaveLength(3);
    expect(decoded.panels[0].kind).toBe("calendar");
  });

  it("customWidgetUrl embeds spec + days + theme as query params", () => {
    const def: WidgetDef = {
      layout: "1-panel",
      panels: [{ kind: "grade" }],
    };
    const url = customWidgetUrl("http://localhost:8080/widget/svg/abc", def, {
      days: 30,
      theme: "dark",
    });
    expect(url).toContain("/custom?spec=");
    expect(url).toContain("&days=30");
    expect(url).toContain("&theme=dark");
  });
});
