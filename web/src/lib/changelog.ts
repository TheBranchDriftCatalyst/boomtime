// Parser for the CHANGELOG.md format produced by our git-cliff template
// (see cliff.toml). Keyed to the deterministic shape:
//
//   # Changelog                       (optional single top-level header, discarded)
//   ...prose (discarded)...
//   ## [<version>] - <YYYY-MM-DD>     ("unreleased" if omitted)
//   ### <group>
//   - <optional **scope:** >item text
//   ...
//
// The FE renders these grouped cards, marking one section as "current" (the
// running app version) and any newer sections as "unreleased/newer than
// running". We hand-rolled this instead of pulling in react-markdown because
// the format is under our control via cliff.toml and it saves ~30KB.

/** A single changelog line under one `###` group. */
export interface ChangelogEntry {
  /** Optional `**scope:**` prefix. */
  scope?: string;
  /** The item text with the leading `- ` and `**scope:**` stripped. */
  message: string;
}

/** All entries under a single `###` group heading. */
export interface ChangelogGroup {
  name: string;
  entries: ChangelogEntry[];
}

/** One release (or the unreleased section) with its groups. */
export interface ChangelogRelease {
  /** The version string with no leading `v`. `"unreleased"` for un-tagged work. */
  version: string;
  /** True iff this section is un-tagged work. */
  unreleased: boolean;
  /** `YYYY-MM-DD` from the git-cliff header; empty for unreleased. */
  date: string;
  groups: ChangelogGroup[];
}

const RELEASE_HEADER = /^##\s+\[([^\]]+)\](?:\s*-\s*(\S+))?/;
const GROUP_HEADER = /^###\s+(.+?)\s*$/;
const LIST_ITEM = /^-\s+(.+?)\s*$/;
const SCOPE_PREFIX = /^\*\*([^*:]+):\*\*\s*(.+)$/;

/**
 * Parse the git-cliff CHANGELOG.md text into a list of releases (newest first,
 * matching cliff.toml's `sort_commits = "newest"`).
 *
 * Robustness rules:
 * - Lines outside a release header are dropped (top prose, the `# Changelog`
 *   banner, git-cliff's footer).
 * - Group entries emitted before any `### <group>` line are attached to a
 *   synthetic "Changes" group so nothing silently disappears.
 * - A malformed release header (missing date) is still preserved as a release
 *   with `date = ""`.
 */
export function parseChangelog(md: string): ChangelogRelease[] {
  const releases: ChangelogRelease[] = [];
  let current: ChangelogRelease | null = null;
  let currentGroup: ChangelogGroup | null = null;

  const lines = md.replace(/\r\n/g, "\n").split("\n");
  for (const raw of lines) {
    const line = raw.trimEnd();
    if (!line) continue;

    const rel = RELEASE_HEADER.exec(line);
    if (rel) {
      const raw = rel[1].trim();
      const unreleased = raw.toLowerCase() === "unreleased";
      current = {
        version: unreleased ? "unreleased" : raw.replace(/^v/, ""),
        unreleased,
        date: rel[2]?.trim() ?? "",
        groups: [],
      };
      currentGroup = null;
      releases.push(current);
      continue;
    }

    if (!current) continue;

    const grp = GROUP_HEADER.exec(line);
    if (grp) {
      currentGroup = { name: grp[1], entries: [] };
      current.groups.push(currentGroup);
      continue;
    }

    const item = LIST_ITEM.exec(line);
    if (item) {
      if (!currentGroup) {
        currentGroup = { name: "Changes", entries: [] };
        current.groups.push(currentGroup);
      }
      const body = item[1];
      const scoped = SCOPE_PREFIX.exec(body);
      if (scoped) {
        currentGroup.entries.push({ scope: scoped[1], message: scoped[2] });
      } else {
        currentGroup.entries.push({ message: body });
      }
    }
    // Any other line inside a release (prose, blank-ish) is intentionally
    // dropped — cliff.toml doesn't produce them today.
  }

  return releases;
}

/**
 * `git describe`-style versions look like `v1.2.3-4-gabcdef` or `v1.2.3` or
 * `abc1234` (no tags yet) or `v1.2.3-dirty`. Extract just the semver `x.y.z`
 * if present so we can match a release header against the running binary.
 * Returns null for un-tagged / SHA-only versions.
 */
export function extractSemver(v: string | undefined | null): string | null {
  if (!v) return null;
  const m = /(?:^v?)(\d+\.\d+\.\d+)/.exec(v);
  return m ? m[1] : null;
}

/** Compare two dotted numeric versions; returns -1 / 0 / 1. */
function compareSemver(a: string, b: string): number {
  const pa = a.split(".").map((x) => parseInt(x, 10));
  const pb = b.split(".").map((x) => parseInt(x, 10));
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const av = pa[i] ?? 0;
    const bv = pb[i] ?? 0;
    if (av !== bv) return av < bv ? -1 : 1;
  }
  return 0;
}

/** Classification used by the UI to badge each release relative to running. */
export type ReleaseStatus = "current" | "newer" | "older" | "unreleased";

/**
 * Classify each release against the running version. `"unreleased"` sections
 * always classify as unreleased. If we can't extract a semver from the running
 * version (fresh clone with no tags), everything classifies as `"older"` and
 * nothing is highlighted — the UI shows the version string verbatim in the
 * header instead.
 */
export function classifyReleases(
  releases: ChangelogRelease[],
  running: string | undefined | null,
): Map<ChangelogRelease, ReleaseStatus> {
  const out = new Map<ChangelogRelease, ReleaseStatus>();
  const runSemver = extractSemver(running);
  for (const r of releases) {
    if (r.unreleased) {
      out.set(r, "unreleased");
      continue;
    }
    if (!runSemver) {
      out.set(r, "older");
      continue;
    }
    const cmp = compareSemver(r.version, runSemver);
    out.set(r, cmp === 0 ? "current" : cmp > 0 ? "newer" : "older");
  }
  return out;
}
