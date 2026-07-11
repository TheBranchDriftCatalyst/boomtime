import { describe, expect, it } from "vitest";
import {
  classifyReleases,
  extractSemver,
  parseChangelog,
} from "./changelog";

const sample = `# Changelog

Prose header we discard.

## [1.2.0] - 2026-04-01

### Features

- **api:** paginate the fooList endpoint
- add the /bar endpoint

### Bug Fixes

- **web:** avoid a hooks-order crash

## [1.1.0] - 2026-03-15

### Features

- **web:** synthwave theme
`;

describe("parseChangelog", () => {
  it("splits releases newest-first with grouped entries", () => {
    const releases = parseChangelog(sample);
    expect(releases.map((r) => r.version)).toEqual(["1.2.0", "1.1.0"]);
    expect(releases[0].date).toBe("2026-04-01");
    expect(releases[0].unreleased).toBe(false);
    expect(releases[0].groups.map((g) => g.name)).toEqual([
      "Features",
      "Bug Fixes",
    ]);
    expect(releases[0].groups[0].entries).toEqual([
      { scope: "api", message: "paginate the fooList endpoint" },
      { message: "add the /bar endpoint" },
    ]);
    expect(releases[0].groups[1].entries).toEqual([
      { scope: "web", message: "avoid a hooks-order crash" },
    ]);
  });

  it("recognizes the unreleased section", () => {
    const md = `# Changelog\n\n## [unreleased]\n\n### Features\n\n- **web:** wip\n`;
    const releases = parseChangelog(md);
    expect(releases).toHaveLength(1);
    expect(releases[0].unreleased).toBe(true);
    expect(releases[0].version).toBe("unreleased");
    expect(releases[0].date).toBe("");
  });

  it("strips the leading v from tagged versions", () => {
    const md = `## [v0.1.0] - 2026-01-01\n\n### Features\n\n- initial\n`;
    const releases = parseChangelog(md);
    expect(releases[0].version).toBe("0.1.0");
  });

  it("attaches orphan list items to a synthetic Changes group", () => {
    const md = `## [1.0.0] - 2026-01-01\n\n- orphan without a group heading\n`;
    const releases = parseChangelog(md);
    expect(releases[0].groups).toEqual([
      { name: "Changes", entries: [{ message: "orphan without a group heading" }] },
    ]);
  });

  it("drops everything outside a release header", () => {
    const md = `# Changelog\n\nblurb\n\n### Features (orphan header pre-release)\n\n- ignored\n\n## [1.0.0] - 2026-01-01\n\n### Features\n\n- kept\n`;
    const releases = parseChangelog(md);
    expect(releases).toHaveLength(1);
    expect(releases[0].groups[0].entries).toEqual([{ message: "kept" }]);
  });
});

describe("extractSemver", () => {
  it.each([
    ["v1.2.3", "1.2.3"],
    ["1.2.3", "1.2.3"],
    ["v1.2.3-4-gabcdef", "1.2.3"],
    ["v1.2.3-dirty", "1.2.3"],
    ["abc1234", null],
    ["dev", null],
    ["", null],
    [undefined, null],
    [null, null],
  ])("%s -> %s", (input, want) => {
    expect(extractSemver(input as string | null | undefined)).toBe(want);
  });
});

describe("classifyReleases", () => {
  const releases = parseChangelog(
    `## [unreleased]\n\n### Features\n\n- wip\n\n## [1.2.0] - 2026-04-01\n\n### Features\n\n- a\n\n## [1.1.0] - 2026-03-15\n\n### Features\n\n- b\n\n## [1.0.0] - 2026-01-01\n\n### Features\n\n- c\n`,
  );

  it("marks the running version 'current' and newer releases 'newer'", () => {
    const cls = classifyReleases(releases, "v1.1.0");
    expect(cls.get(releases[0])).toBe("unreleased"); // unreleased
    expect(cls.get(releases[1])).toBe("newer"); // 1.2.0 > 1.1.0
    expect(cls.get(releases[2])).toBe("current"); // 1.1.0 == 1.1.0
    expect(cls.get(releases[3])).toBe("older"); // 1.0.0 < 1.1.0
  });

  it("classifies everything as 'older' when the running version has no semver", () => {
    const cls = classifyReleases(releases, "abc1234");
    expect(cls.get(releases[1])).toBe("older");
    expect(cls.get(releases[2])).toBe("older");
    // unreleased always wins
    expect(cls.get(releases[0])).toBe("unreleased");
  });

  it("tolerates a git-describe suffix on the running version", () => {
    const cls = classifyReleases(releases, "v1.1.0-3-gabc");
    expect(cls.get(releases[2])).toBe("current");
    expect(cls.get(releases[1])).toBe("newer");
  });
});
