import { describe, expect, it } from "vitest";
import {
  remapDisplay,
  templateToBackend,
  templateToJs,
} from "@/lib/remapDisplay";
import type { CurationRule } from "@/types/api";

function rule(over: Partial<CurationRule>): CurationRule {
  return {
    id: 1,
    axis: "project",
    action: "rename",
    matchValue: "",
    newValue: null,
    matchType: "exact",
    createdAt: "2026-07-01T00:00:00Z",
    ...over,
  };
}

describe("template backref converters", () => {
  it("templateToJs: `\\1` -> `$1`", () => {
    expect(templateToJs("\\1")).toBe("$1");
    expect(templateToJs("pre-\\1-\\2")).toBe("pre-$1-$2");
  });

  it("templateToBackend: `$1` -> `\\1`, `$$` -> `$`", () => {
    expect(templateToBackend("$1")).toBe("\\1");
    expect(templateToBackend("a$1b$2")).toBe("a\\1b\\2");
    expect(templateToBackend("cost $$5")).toBe("cost $5");
  });

  it("round-trips a template through backend then JS form", () => {
    // UI "$1" -> backend "\\1" -> JS "$1"
    expect(templateToJs(templateToBackend("$1"))).toBe("$1");
  });
});

describe("remapDisplay", () => {
  it("null value or no rules -> null", () => {
    expect(remapDisplay("project", null, [])).toBeNull();
    expect(remapDisplay("project", "x", undefined)).toBeNull();
    expect(remapDisplay("project", "x", [])).toBeNull();
  });

  it("exact rule maps the literal value", () => {
    const rules = [rule({ matchValue: "gaka", newValue: "gakatime" })];
    expect(remapDisplay("project", "gaka", rules)).toBe("gakatime");
    expect(remapDisplay("project", "other", rules)).toBeNull();
  });

  it("ignores rules for a different axis or action", () => {
    const rules = [
      rule({ axis: "language", matchValue: "x", newValue: "Y" }),
      rule({ action: "hide", matchValue: "x", newValue: null }),
    ];
    expect(remapDisplay("project", "x", rules)).toBeNull();
  });

  it("regex rule: matching value -> newValue", () => {
    const rules = [
      rule({ matchType: "regex", matchValue: "^Meet", newValue: "Meeting" }),
    ];
    expect(remapDisplay("project", "Meet - Weekly", rules)).toBe("Meeting");
    expect(remapDisplay("project", "Standup", rules)).toBeNull();
  });

  it("template rule: strips a leading @ via ^@(.*)$ + \\1", () => {
    const rules = [
      rule({
        matchType: "template",
        matchValue: "^@(.*)$",
        newValue: "\\1", // backend stores `\1`
      }),
    ];
    expect(remapDisplay("project", "@swarm-graph", rules)).toBe("swarm-graph");
    // No leading @ -> pattern doesn't match -> null.
    expect(remapDisplay("project", "gakatime", rules)).toBeNull();
  });

  it("template rule with a literal prefix template", () => {
    const rules = [
      rule({
        matchType: "template",
        matchValue: "^prefix-(.*)$",
        newValue: "team/\\1",
      }),
    ];
    expect(remapDisplay("project", "prefix-alpha", rules)).toBe("team/alpha");
  });

  it("precedence: exact wins over a regex that would also match", () => {
    const rules = [
      rule({ matchType: "regex", matchValue: ".*", newValue: "FromRegex" }),
      rule({ matchType: "exact", matchValue: "gaka", newValue: "FromExact" }),
    ];
    expect(remapDisplay("project", "gaka", rules)).toBe("FromExact");
  });

  it("invalid regex is skipped, not thrown", () => {
    const rules = [
      rule({ matchType: "regex", matchValue: "(unclosed", newValue: "X" }),
    ];
    expect(remapDisplay("project", "anything", rules)).toBeNull();
  });
});
