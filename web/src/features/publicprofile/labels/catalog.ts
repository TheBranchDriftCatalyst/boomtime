// catalog.ts — the MVP label manifest (gaka-364).
//
// This file IS the DSL manifest — kept in TypeScript on purpose so the
// Condition discriminated union in ./types.ts drives IDE autocomplete
// on every field and typos become compile errors instead of silent
// no-match at award time. Adding a new label = one object literal
// below (or one `...tierLabels(...)` spread for a full 5-tier ladder).
//
// (Considered YAML + a runtime validator; rejected — the ergonomic win
// is "add a label without opening the type file", but authors need to
// know the shape either way, and TS gives that for free via the type
// annotation on LABEL_CATALOG.)
//
// Guidelines for maintainers:
//   - Give every archetype/tribe a unique, memorable name. Users see
//     these on their public profile.
//   - Rank ranges: tier ladders 100-199, archetypes 50-99, tribes 20-49.
//     Higher = shown first on the hero tagline (top-3).
//   - Thresholds should feel EARNED, not participatory. If half your
//     userbase would qualify for LEGEND, tune it up.
import type { LabelSpec } from "./types";
import { tierLabels } from "./tierLabels";

export const LABEL_CATALOG: LabelSpec[] = [
  // ---------------- TIER LADDERS (5 per axis-value) ----------------------
  // Language tiers — hand-picked hot languages. The catalog can carry
  // more; MVP covers the common set that boomtime users file first.
  ...tierLabels({
    axis: "languages",
    value: "python",
    glyph: "🐍",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  }),
  ...tierLabels({
    axis: "languages",
    value: "typescript",
    glyph: "🅃🅂",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  }),
  ...tierLabels({
    axis: "languages",
    value: "javascript",
    glyph: "🟨",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  }),
  ...tierLabels({
    axis: "languages",
    value: "go",
    glyph: "🐹",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  }),
  ...tierLabels({
    axis: "languages",
    value: "rust",
    glyph: "🦀",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  }),
  // Editor tiers — same ladder, different axis.
  ...tierLabels({
    axis: "editors",
    value: "vim",
    glyph: "▌",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  }),
  ...tierLabels({
    axis: "editors",
    value: "neovim",
    glyph: "◆",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  }),
  ...tierLabels({
    axis: "editors",
    value: "vscode",
    glyph: "🅥",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  }),
  ...tierLabels({
    axis: "editors",
    value: "emacs",
    glyph: "𝓔",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  }),

  // ---------------- ARCHETYPES (personality — can hold many) --------------
  {
    id: "late-night-coder",
    kind: "archetype",
    label: "LATE NIGHT CODER",
    glyph: "🌙",
    description: "≥25% of activity between 10pm and 3am",
    rank: 80,
    condition: {
      kind: "punchcard-hour-pct",
      hoursIn: [22, 23, 0, 1, 2, 3],
      op: ">=",
      pct: 0.25,
    },
  },
  {
    id: "early-bird",
    kind: "archetype",
    label: "EARLY BIRD",
    glyph: "🌅",
    description: "≥25% of activity between 5am and 9am",
    rank: 80,
    condition: {
      kind: "punchcard-hour-pct",
      hoursIn: [5, 6, 7, 8, 9],
      op: ">=",
      pct: 0.25,
    },
  },
  {
    id: "weekend-warrior",
    kind: "archetype",
    label: "WEEKEND WARRIOR",
    glyph: "⚔",
    description: "≥30% of activity on Saturday or Sunday",
    rank: 75,
    condition: {
      kind: "punchcard-dow-pct",
      dowIn: [0, 6], // Sun, Sat
      op: ">=",
      pct: 0.3,
    },
  },
  {
    id: "monogamist",
    kind: "archetype",
    label: "MONOGAMIST",
    glyph: "💍",
    description: "Top project accounts for ≥70% of coding time",
    rank: 70,
    condition: { kind: "top-share", axis: "projects", op: ">=", pct: 0.7 },
  },
  {
    id: "polyglot",
    kind: "archetype",
    label: "POLYGLOT",
    glyph: "🗣",
    description: "5+ languages each with ≥5h in the range",
    rank: 85,
    condition: {
      kind: "distinct-count",
      axis: "languages",
      minHoursEach: 5,
      op: ">=",
      n: 5,
    },
  },
  {
    id: "consistent",
    kind: "archetype",
    label: "CONSISTENT",
    glyph: "🔥",
    description: "Current streak ≥30 days",
    rank: 90,
    condition: { kind: "streak", which: "current", op: ">=", days: 30 },
  },
  {
    id: "sprinter",
    kind: "archetype",
    label: "SPRINTER",
    glyph: "🚀",
    description: "Last 7 days averaged ≥2× the prior 7 days",
    rank: 70,
    condition: { kind: "trend", window: "last7-vs-prior7", op: ">=", ratio: 2.0 },
  },
  {
    id: "machine",
    kind: "archetype",
    label: "MACHINE",
    glyph: "🤖",
    description: "Daily average ≥3h",
    rank: 88,
    condition: { kind: "daily-avg", op: ">=", hours: 3 },
  },
  {
    id: "deep-focus",
    kind: "archetype",
    label: "DEEP FOCUS",
    glyph: "🎯",
    description: "Top project holds ≥80% share (extreme concentration)",
    rank: 75,
    condition: { kind: "top-share", axis: "projects", op: ">=", pct: 0.8 },
  },
  {
    id: "multi-tasker",
    kind: "archetype",
    label: "MULTI TASKER",
    glyph: "🔀",
    description: "5+ projects each with ≥5h in the range",
    rank: 70,
    condition: {
      kind: "distinct-count",
      axis: "projects",
      minHoursEach: 5,
      op: ">=",
      n: 5,
    },
  },
  {
    id: "meeting-warrior",
    kind: "archetype",
    label: "MEETING WARRIOR",
    glyph: "📞",
    description: "Meetings account for ≥10% of tracked time",
    rank: 60,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "meeting",
      op: ">=",
      pct: 0.1,
    },
  },
  {
    id: "ai-native",
    kind: "archetype",
    label: "AI NATIVE",
    glyph: "✨",
    description: "AI-assisted coding ≥25% of tracked time",
    rank: 75,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "ai coding",
      op: ">=",
      pct: 0.25,
    },
  },
  {
    id: "test-obsessive",
    kind: "archetype",
    label: "TEST OBSESSIVE",
    glyph: "✅",
    description: "Test-writing ≥5% of tracked time",
    rank: 65,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "writing tests",
      op: ">=",
      pct: 0.05,
    },
  },
  {
    id: "documenter",
    kind: "archetype",
    label: "DOCUMENTER",
    glyph: "📝",
    description: "Doc-writing ≥5% of tracked time",
    rank: 60,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "writing docs",
      op: ">=",
      pct: 0.05,
    },
  },

  // ---------------- TRIBES (community identity) --------------------------
  {
    id: "vim-enjoyer",
    kind: "tribe",
    label: "VIM ENJOYER",
    glyph: "▌",
    description: "≥10h in Vim",
    rank: 30,
    condition: { kind: "axis-time", axis: "editors", value: "vim", op: ">=", hours: 10 },
  },
  {
    id: "emacs-elder",
    kind: "tribe",
    label: "EMACS ELDER",
    glyph: "𝓔",
    description: "≥100h in Emacs",
    rank: 35,
    condition: { kind: "axis-time", axis: "editors", value: "emacs", op: ">=", hours: 100 },
  },
  {
    id: "terminal-purist",
    kind: "tribe",
    label: "TERMINAL PURIST",
    glyph: "❯",
    description: "Vim + Neovim + Emacs together ≥90% of editor time",
    rank: 40,
    // No single primitive says "sum(v1,v2,v3) / total ≥ pct". Cheapest
    // approximation with the current DSL: at least ONE of the three must
    // clear the 90% share of the editors axis. If a user splits 50/50
    // between Vim and Neovim, neither hits 90% alone; that's a real
    // false-negative and a good candidate for a `sum-share` primitive
    // later. Documenting the seam here rather than sneaking in extra
    // conditional code.
    condition: {
      kind: "any",
      of: [
        { kind: "axis-pct", axis: "editors", value: "vim", op: ">=", pct: 0.9 },
        { kind: "axis-pct", axis: "editors", value: "neovim", op: ">=", pct: 0.9 },
        { kind: "axis-pct", axis: "editors", value: "emacs", op: ">=", pct: 0.9 },
      ],
    },
  },
  {
    id: "mac-native",
    kind: "tribe",
    label: "MAC NATIVE",
    glyph: "🍎",
    description: "≥200h on macOS",
    rank: 25,
    condition: { kind: "axis-time", axis: "platforms", value: "mac", op: ">=", hours: 200 },
  },
  {
    id: "linux-warlord",
    kind: "tribe",
    label: "LINUX WARLORD",
    glyph: "🐧",
    description: "≥200h on Linux",
    rank: 25,
    condition: { kind: "axis-time", axis: "platforms", value: "linux", op: ">=", hours: 200 },
  },
  {
    id: "windows-survivor",
    kind: "tribe",
    label: "WINDOWS SURVIVOR",
    glyph: "🪟",
    description: "≥200h on Windows",
    rank: 25,
    condition: { kind: "axis-time", axis: "platforms", value: "windows", op: ">=", hours: 200 },
  },
  {
    id: "cross-platform",
    kind: "tribe",
    label: "CROSS PLATFORM",
    glyph: "🔗",
    description: "2+ platforms each with ≥50h",
    rank: 45,
    condition: {
      kind: "distinct-count",
      axis: "platforms",
      minHoursEach: 50,
      op: ">=",
      n: 2,
    },
  },
];
