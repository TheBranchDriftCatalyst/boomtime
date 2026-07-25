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

  // ============================================================================
  // MEMECORE EXPANSION (gaka-364.1) — "gimme the OP shiznit commander neko paws"
  // ============================================================================
  //
  // Everything below is `kind: "meme"` and lives in the 100-199 rank band so
  // it wins the hero top-3 slot against the tame archetypes above. Entries
  // are APPEND-ONLY on purpose: the gaka-myv image-worker agent reads this
  // file for prompts, and reordering would invalidate their diff. Add new
  // labels at the bottom.
  //
  // Categories represented (loosely, so the manifest stays scannable):
  //   § GRINDSET     — activity-volume kings (sigma, gigachad, alpha, hustle)
  //   § SPACE MARINE — WH40k military parody (imperial fist, chapter master)
  //   § KAWAII / UWU — cute anime, respectful (neko paws, tsundere, kawaii mage)
  //   § BRAINROT     — Twitch chat / streamer slang (poggers, based, sigma male)
  //   § BUSHIDO      — terminal / editor purist upgrades (vim bushido, tmux warlord)
  //   § OS TRIBES++  — upgrade tiers of the base tribes (arch btw, mac warlord)
  //   § CATEGORY OP  — category-axis power fantasies (prompt supreme, kubernetes cultist)
  //   § META         — composed labels that require several sub-conditions
  //
  // Rank policy (see catalog.test.ts invariant):
  //   150-179 → meta-labels (COMMANDER NEKO PAWS / TRUE GRINDSET / BASED CHAD ULTIMATE)
  //   130-149 → OP grindset + space marine flagships
  //   110-129 → kawaii signature, brainrot flagships
  //   100-109 → OP archetype variants, tribe upgrades
  //
  // Prompt style (for gaka-myv Illustrious XL / anime pipeline):
  //   cyberpunk anime, chibi half-body emblem, red/black Arasaka palette,
  //   NO TEXT (Illustrious garbles text). Vary the character archetype
  //   per label so images stay distinct at thumbnail size.

  // -- § GRINDSET ------------------------------------------------------------
  {
    id: "sigma-grindset",
    kind: "meme",
    label: "SIGMA GRINDSET",
    glyph: "Σ",
    description: "Daily average ≥6h — the lone-wolf sigma trajectory",
    rank: 135,
    condition: { kind: "daily-avg", op: ">=", hours: 6 },
    imagePrompt:
      "cyberpunk anime male sigma grindset commander, chiseled jawline, black tactical suit with red accents, arms crossed, moonlit rooftop terminal glow, chibi half-body emblem, dark background, no text",
  },
  {
    id: "gigachad-committer",
    kind: "meme",
    label: "GIGACHAD COMMITTER",
    glyph: "◉",
    description: "Current streak ≥28 days (four straight weeks unbroken)",
    rank: 130,
    condition: { kind: "streak", which: "current", op: ">=", days: 28 },
    imagePrompt:
      "cyberpunk anime gigachad developer with red glowing keyboard aura, marble-jawline chibi half-body emblem, black hoodie with red circuit trim, dark background, no text",
  },
  {
    id: "alpha-mogger",
    kind: "meme",
    label: "ALPHA MOGGER",
    glyph: "▲",
    description: "Top project holds ≥90% share — total tunnel-vision",
    rank: 125,
    condition: { kind: "top-share", axis: "projects", op: ">=", pct: 0.9 },
    imagePrompt:
      "cyberpunk anime alpha operator gazing into a single glowing red monitor, laser focus, chibi half-body emblem, dark background, no text",
  },
  {
    id: "mewing-master",
    kind: "meme",
    label: "MEWING MASTER",
    glyph: "◐",
    description: "≥30% of activity during silent-mew hours (5am-7am)",
    rank: 115,
    condition: {
      kind: "punchcard-hour-pct",
      hoursIn: [5, 6, 7],
      op: ">=",
      pct: 0.3,
    },
    imagePrompt:
      "cyberpunk anime early-morning ninja monk mewing at a dark terminal, dawn light bleeding red across the room, chibi half-body emblem, no text",
  },
  {
    id: "hustle-android",
    kind: "meme",
    label: "HUSTLE ANDROID",
    glyph: "☗",
    description: "Daily average ≥8h — post-human throughput",
    rank: 140,
    condition: { kind: "daily-avg", op: ">=", hours: 8 },
    imagePrompt:
      "cyberpunk anime android developer with visible red circuitry through translucent skin, plugged directly into a terminal, chibi half-body emblem, dark background, no text",
  },
  {
    id: "sleep-is-a-construct",
    kind: "meme",
    label: "SLEEP-IS-A-CONSTRUCT",
    glyph: "☾",
    description: "≥40% of activity between midnight and 5am",
    rank: 125,
    condition: {
      kind: "punchcard-hour-pct",
      hoursIn: [0, 1, 2, 3, 4],
      op: ">=",
      pct: 0.4,
    },
    imagePrompt:
      "cyberpunk anime insomniac hacker with dark circles under glowing red eyes, hunched at monitor at 3am, chibi half-body emblem, dark background, no text",
  },
  {
    id: "weekend-warlord",
    kind: "meme",
    label: "WEEKEND WARLORD",
    glyph: "⚔",
    description: "≥50% of activity on Saturday or Sunday",
    rank: 120,
    condition: {
      kind: "punchcard-dow-pct",
      dowIn: [0, 6],
      op: ">=",
      pct: 0.5,
    },
    imagePrompt:
      "cyberpunk anime weekend warrior samurai wielding a red katana over a keyboard altar, chibi half-body emblem, dark background, no text",
  },

  // -- § SPACE MARINE / WH40K -----------------------------------------------
  {
    id: "space-marine",
    kind: "meme",
    label: "SPACE MARINE",
    glyph: "☩",
    description:
      "Daily-avg ≥3h AND workday hours (9am-3pm) hold ≤60% of the punchcard — 24/7 coverage",
    rank: 130,
    // Two conjuncts on purpose:
    //   1. daily-avg ≥3h — gates on "there IS activity" so an empty payload
    //      can't trip the label via `not` over a zero-denominator punchcard.
    //   2. `not(workday-block ≥60%)` — the spread signal. If the standard
    //      9am-3pm window does NOT dominate, activity is scattered across
    //      the rest of the day like a space-marine standing watch.
    condition: {
      kind: "all",
      of: [
        { kind: "daily-avg", op: ">=", hours: 3 },
        {
          kind: "not",
          of: {
            kind: "punchcard-hour-pct",
            hoursIn: [9, 10, 11, 12, 13, 14],
            op: ">=",
            pct: 0.6,
          },
        },
      ],
    },
    imagePrompt:
      "warhammer 40k space marine anime chibi in power armor, deep red trim, purity seals hanging, keyboard mounted to bolter, half-body emblem, dark background, no text",
  },
  {
    id: "imperial-fist",
    kind: "meme",
    label: "IMPERIAL FIST",
    glyph: "✊",
    description: "Longest streak ≥60 days — Dorn would be proud",
    rank: 130,
    condition: { kind: "streak", which: "longest", op: ">=", days: 60 },
    imagePrompt:
      "warhammer 40k imperial fist space marine anime chibi in yellow power armor with red trim, fist raised, half-body emblem, dark background, no text",
  },
  {
    id: "battle-brother-of-the-keyboard",
    kind: "meme",
    label: "BATTLE-BROTHER OF THE KEYBOARD",
    glyph: "⛨",
    description: "Longest streak ≥200 days — a battle-brother forged in code",
    rank: 145,
    condition: { kind: "streak", which: "longest", op: ">=", days: 200 },
    imagePrompt:
      "warhammer 40k space marine anime chibi with battle-worn power armor gripping a mechanical keyboard as a bolt weapon, red accents, purity seals, half-body emblem, dark background, no text",
  },
  {
    id: "for-the-emperor",
    kind: "meme",
    label: "FOR THE EMPEROR",
    glyph: "♆",
    description: "Top language holds ≥90% share — devotion, not diversity",
    rank: 125,
    condition: { kind: "top-share", axis: "languages", op: ">=", pct: 0.9 },
    imagePrompt:
      "warhammer 40k anime chibi zealot brandishing a scroll of glowing red source code, imperial iconography, half-body emblem, dark background, no text",
  },
  {
    id: "chapter-master",
    kind: "meme",
    label: "CHAPTER MASTER",
    glyph: "☬",
    description: "One project has ≥500h — you built a chapter",
    rank: 140,
    condition: {
      kind: "distinct-count",
      axis: "projects",
      minHoursEach: 500,
      op: ">=",
      n: 1,
    },
    imagePrompt:
      "warhammer 40k anime chibi chapter master in ornate power armor holding a data-slate glowing red, cape, purity seals, half-body emblem, dark background, no text",
  },
  {
    id: "dreadnought-pilot",
    kind: "meme",
    label: "DREADNOUGHT PILOT",
    glyph: "◈",
    description: "Daily avg ≥3h AND longest streak ≥180 days — interred veteran",
    rank: 135,
    condition: {
      kind: "all",
      of: [
        { kind: "daily-avg", op: ">=", hours: 3 },
        { kind: "streak", which: "longest", op: ">=", days: 180 },
      ],
    },
    imagePrompt:
      "warhammer 40k dreadnought anime chibi with a small pilot visible through the sarcophagus viewport, red accents, half-body emblem, dark background, no text",
  },
  {
    id: "lord-solar",
    kind: "meme",
    label: "LORD SOLAR",
    glyph: "☀",
    description: "Daily avg ≥12h — legendary post-human throughput",
    rank: 149,
    condition: { kind: "daily-avg", op: ">=", hours: 12 },
    imagePrompt:
      "warhammer 40k lord solar anime chibi with solar-crown halo of red plasma, ornate uniform, commanding gesture, half-body emblem, dark background, no text",
  },
  {
    id: "inquisitor",
    kind: "meme",
    label: "INQUISITOR",
    glyph: "☒",
    description: "8+ distinct projects each with ≥5h — nothing escapes scrutiny",
    rank: 120,
    condition: {
      kind: "distinct-count",
      axis: "projects",
      minHoursEach: 5,
      op: ">=",
      n: 8,
    },
    imagePrompt:
      "warhammer 40k inquisitor anime chibi with red inquisitorial rosette, black trenchcoat, glowing runes floating around, half-body emblem, dark background, no text",
  },

  // -- § KAWAII / UWU / NYAA ------------------------------------------------
  {
    id: "kawaii-warlord",
    kind: "meme",
    label: "KAWAII WARLORD",
    glyph: "❀",
    description: "Design category ≥15% of tracked time — pixel-perfect nyaa",
    rank: 120,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "designing",
      op: ">=",
      pct: 0.15,
    },
    imagePrompt:
      "kawaii anime warlord girl with cat ears, pastel pink and red armor, wielding a stylus like a sword, chibi half-body emblem, dark background, no text",
  },
  {
    id: "tsundere-compiler",
    kind: "meme",
    label: "TSUNDERE COMPILER",
    glyph: "!?",
    description: "≥100h TypeScript — it's not like I like static types, baka",
    rank: 115,
    condition: {
      kind: "axis-time",
      axis: "languages",
      value: "typescript",
      op: ">=",
      hours: 100,
    },
    imagePrompt:
      "kawaii tsundere anime girl at a keyboard, blushing while yelling at a compiler window, chibi half-body emblem, red-black palette, no text",
  },
  {
    id: "yandere-debugger",
    kind: "meme",
    label: "YANDERE DEBUGGER",
    glyph: "✂",
    description: "Debugging category ≥10% of tracked time — obsessive fixation",
    rank: 115,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "debugging",
      op: ">=",
      pct: 0.1,
    },
    imagePrompt:
      "kawaii yandere anime girl with dilated red eyes clutching a bug net, sinister smile, chibi half-body emblem, dark background, no text",
  },
  {
    id: "catboy-operator",
    kind: "meme",
    label: "CATBOY OPERATOR",
    glyph: "≽^•⩊•^≼",
    description: "≥50h in Vim AND ≥50h on Linux — the terminal-purist crossover",
    rank: 125,
    condition: {
      kind: "all",
      of: [
        { kind: "axis-time", axis: "editors", value: "vim", op: ">=", hours: 50 },
        { kind: "axis-time", axis: "platforms", value: "linux", op: ">=", hours: 50 },
      ],
    },
    imagePrompt:
      "cyberpunk anime catboy operator with cat ears and tail, thigh-highs and a black hoodie, glowing red terminal reflection on face, chibi half-body emblem, dark background, no text",
  },
  {
    id: "femboy-fortress",
    kind: "meme",
    label: "FEMBOY FORTRESS",
    glyph: "♡",
    description: "≥500h on Linux — the fortress holds",
    rank: 125,
    condition: {
      kind: "axis-time",
      axis: "platforms",
      value: "linux",
      op: ">=",
      hours: 500,
    },
    imagePrompt:
      "kawaii cyberpunk femboy operator inside a fortified server room lined with red LED racks, holding a mechanical keyboard, chibi half-body emblem, dark background, no text",
  },
  {
    id: "kawaii-code-mage",
    kind: "meme",
    label: "KAWAII CODE MAGE",
    glyph: "✿",
    description: "≥100h Python AND AI-coding ≥20% — spellcasting with familiars",
    rank: 130,
    condition: {
      kind: "all",
      of: [
        { kind: "axis-time", axis: "languages", value: "python", op: ">=", hours: 100 },
        { kind: "axis-pct", axis: "categories", value: "ai coding", op: ">=", pct: 0.2 },
      ],
    },
    imagePrompt:
      "kawaii anime witch girl coding at a magical crystal terminal, red pastel palette, cat familiar with glowing eyes on her shoulder, chibi half-body emblem, dark background, no text",
  },
  {
    id: "maid-cafe-manager",
    kind: "meme",
    label: "MAID CAFE MANAGER",
    glyph: "☕",
    description: "Meetings ≥10% AND 5+ languages ≥5h — the polite polyglot manager",
    rank: 115,
    condition: {
      kind: "all",
      of: [
        { kind: "axis-pct", axis: "categories", value: "meeting", op: ">=", pct: 0.1 },
        {
          kind: "distinct-count",
          axis: "languages",
          minHoursEach: 5,
          op: ">=",
          n: 5,
        },
      ],
    },
    imagePrompt:
      "kawaii anime maid manager with red hair-ribbon serving a tray of glowing tokens to five little developer chibis, half-body emblem, dark background, no text",
  },
  {
    id: "commander-neko-paws",
    kind: "meme",
    label: "COMMANDER NEKO PAWS",
    glyph: "🐾",
    description:
      "≥6h daily avg AND streak ≥60 AND top-share ≥70% — the OP-est overall (rare)",
    rank: 170,
    condition: {
      kind: "all",
      of: [
        { kind: "daily-avg", op: ">=", hours: 6 },
        { kind: "streak", which: "current", op: ">=", days: 60 },
        { kind: "top-share", axis: "projects", op: ">=", pct: 0.7 },
      ],
    },
    imagePrompt:
      "cyberpunk anime commander with cat ears and cat paws, tactical uniform with red glowing insignia, holographic tactical display floating in front, chibi half-body emblem, dark background, no text",
  },

  // -- § GAMER BRAINROT -----------------------------------------------------
  {
    id: "poggers-committer",
    kind: "meme",
    label: "POGGERS COMMITTER",
    glyph: "◎",
    description: "Last 7 days averaged ≥2× the prior 7 — heating up",
    rank: 115,
    condition: { kind: "trend", window: "last7-vs-prior7", op: ">=", ratio: 2.0 },
    imagePrompt:
      "cyberpunk anime streamer at RGB mechanical keyboard, exaggerated POGGERS surprised face, twitch chat overlay glowing red, chibi half-body emblem, dark background, no text",
  },
  {
    id: "omegalul-warlock",
    kind: "meme",
    label: "OMEGALUL WARLOCK",
    glyph: "≋",
    description: "≥80% top-project share AND ≥50% weekend activity — cursed devotion",
    rank: 125,
    condition: {
      kind: "all",
      of: [
        { kind: "top-share", axis: "projects", op: ">=", pct: 0.8 },
        {
          kind: "punchcard-dow-pct",
          dowIn: [0, 6],
          op: ">=",
          pct: 0.5,
        },
      ],
    },
    imagePrompt:
      "cyberpunk anime warlock casting a giant OMEGALUL sigil in glowing red, weekend rain outside the window, chibi half-body emblem, dark background, no text",
  },
  {
    id: "copium-connoisseur",
    kind: "meme",
    label: "COPIUM CONNOISSEUR",
    glyph: "☁",
    description: "AI-coding ≥35% of tracked time — main-lining the copium",
    rank: 115,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "ai coding",
      op: ">=",
      pct: 0.35,
    },
    imagePrompt:
      "cyberpunk anime figure inhaling from a red neon COPIUM tank connected to a terminal, chibi half-body emblem, dark background, no text",
  },
  {
    id: "malding-supreme",
    kind: "meme",
    label: "MALDING SUPREME",
    glyph: "≠",
    description: "8+ projects ≥5h each — spread so thin you're malding",
    rank: 110,
    condition: {
      kind: "distinct-count",
      axis: "projects",
      minHoursEach: 5,
      op: ">=",
      n: 8,
    },
    imagePrompt:
      "cyberpunk anime bald developer surrounded by eight glowing red project windows, veins visible, chibi half-body emblem, dark background, no text",
  },
  {
    id: "based-department",
    kind: "meme",
    label: "BASED DEPARTMENT",
    glyph: "◐",
    description: "≥100h in Go OR Rust — you called, we answered",
    rank: 115,
    condition: {
      kind: "any",
      of: [
        { kind: "axis-time", axis: "languages", value: "go", op: ">=", hours: 100 },
        { kind: "axis-time", axis: "languages", value: "rust", op: ">=", hours: 100 },
      ],
    },
    imagePrompt:
      "cyberpunk anime operator answering a red vintage phone labeled BASED DEPARTMENT, chibi half-body emblem, dark background, no text",
  },
  {
    id: "chad-developer",
    kind: "meme",
    label: "CHAD DEVELOPER",
    glyph: "♞",
    description: "Top project ≥80% share AND current streak ≥60 — the CHAD arc",
    rank: 130,
    condition: {
      kind: "all",
      of: [
        { kind: "top-share", axis: "projects", op: ">=", pct: 0.8 },
        { kind: "streak", which: "current", op: ">=", days: 60 },
      ],
    },
    imagePrompt:
      "cyberpunk anime chad developer with square jaw, black tank top, arms crossed at a red glowing terminal, chibi half-body emblem, dark background, no text",
  },
  {
    id: "sigma-male",
    kind: "meme",
    label: "SIGMA MALE",
    glyph: "σ",
    description: "Top project ≥95% share — the lone-wolf monogamist supreme",
    rank: 125,
    condition: { kind: "top-share", axis: "projects", op: ">=", pct: 0.95 },
    imagePrompt:
      "cyberpunk anime lone wolf sigma male standing at the edge of a rooftop with a single glowing red terminal open, chibi half-body emblem, dark background, no text",
  },
  {
    id: "rizz-lord",
    kind: "meme",
    label: "RIZZ LORD",
    glyph: "♛",
    description: "AI-coding ≥25% AND design ≥10% — pure aesthetic rizz",
    rank: 120,
    condition: {
      kind: "all",
      of: [
        { kind: "axis-pct", axis: "categories", value: "ai coding", op: ">=", pct: 0.25 },
        { kind: "axis-pct", axis: "categories", value: "designing", op: ">=", pct: 0.1 },
      ],
    },
    imagePrompt:
      "cyberpunk anime rizz lord with sunglasses reflecting red code, gold chain, chibi half-body emblem, dark background, no text",
  },

  // -- § TERMINAL / EDITOR BUSHIDO ------------------------------------------
  {
    id: "vim-bushido",
    kind: "meme",
    label: "VIM BUSHIDO",
    glyph: "刀",
    description: "≥50h in Vim — the way of the modal warrior",
    rank: 110,
    condition: { kind: "axis-time", axis: "editors", value: "vim", op: ">=", hours: 50 },
    imagePrompt:
      "cyberpunk anime samurai monk with a red-lacquered vim helmet, meditating over a glowing terminal katana, chibi half-body emblem, dark background, no text",
  },
  {
    id: "emacs-overlord",
    kind: "meme",
    label: "EMACS OVERLORD",
    glyph: "𝓔",
    description: "≥200h in Emacs — you bent the editor to your will",
    rank: 115,
    condition: {
      kind: "axis-time",
      axis: "editors",
      value: "emacs",
      op: ">=",
      hours: 200,
    },
    imagePrompt:
      "cyberpunk anime overlord in flowing red robes commanding a floating emacs sigil, chibi half-body emblem, dark background, no text",
  },
  {
    id: "neovim-daimyo",
    kind: "meme",
    label: "NEOVIM DAIMYO",
    glyph: "◆",
    description: "≥50h in Neovim — daimyo of the plugin ecosystem",
    rank: 110,
    condition: {
      kind: "axis-time",
      axis: "editors",
      value: "neovim",
      op: ">=",
      hours: 50,
    },
    imagePrompt:
      "cyberpunk anime daimyo with red kabuto helmet marked with a diamond sigil, standing over glowing plugin scrolls, chibi half-body emblem, dark background, no text",
  },
  {
    id: "helix-prophet",
    kind: "meme",
    label: "HELIX PROPHET",
    glyph: "⌇",
    description: "≥20h in Helix — early adopter of the post-vim gospel",
    rank: 105,
    condition: {
      kind: "axis-time",
      axis: "editors",
      value: "helix",
      op: ">=",
      hours: 20,
    },
    imagePrompt:
      "cyberpunk anime prophet with double-helix red halo behind their head, holding an ancient scroll marked with modal-editor runes, chibi half-body emblem, dark background, no text",
  },
  {
    id: "tmux-warlord",
    kind: "meme",
    label: "TMUX WARLORD",
    glyph: "⧉",
    description: "Terminal-editor share ≥90% — you never leave the multiplexer",
    rank: 115,
    condition: {
      kind: "any",
      of: [
        { kind: "axis-pct", axis: "editors", value: "vim", op: ">=", pct: 0.9 },
        { kind: "axis-pct", axis: "editors", value: "neovim", op: ">=", pct: 0.9 },
        { kind: "axis-pct", axis: "editors", value: "emacs", op: ">=", pct: 0.9 },
        { kind: "axis-pct", axis: "editors", value: "helix", op: ">=", pct: 0.9 },
      ],
    },
    imagePrompt:
      "cyberpunk anime warlord on a throne of stacked terminal panes, red glow bleeding between panes, chibi half-body emblem, dark background, no text",
  },
  {
    id: "alacritty-devout",
    kind: "meme",
    label: "ALACRITTY DEVOUT",
    glyph: "≡",
    description: "≥100h terminal-editor use — the alacritty devout",
    rank: 105,
    condition: {
      kind: "any",
      of: [
        { kind: "axis-time", axis: "editors", value: "vim", op: ">=", hours: 100 },
        { kind: "axis-time", axis: "editors", value: "neovim", op: ">=", hours: 100 },
      ],
    },
    imagePrompt:
      "cyberpunk anime devotee kneeling before a glowing red terminal icon on an altar, chibi half-body emblem, dark background, no text",
  },

  // -- § OS TRIBES++  (upgrade tiers of the base tribes) --------------------
  {
    id: "mac-warlord",
    kind: "meme",
    label: "MAC WARLORD",
    glyph: "⌘",
    description: "≥500h on macOS — mac warlord tier",
    rank: 110,
    condition: {
      kind: "axis-time",
      axis: "platforms",
      value: "mac",
      op: ">=",
      hours: 500,
    },
    imagePrompt:
      "cyberpunk anime warlord in silver-and-red armor with a glowing apple sigil, chibi half-body emblem, dark background, no text",
  },
  {
    id: "linux-emperor",
    kind: "meme",
    label: "LINUX EMPEROR",
    glyph: "♔",
    description: "≥500h on Linux — the emperor tier",
    rank: 110,
    condition: {
      kind: "axis-time",
      axis: "platforms",
      value: "linux",
      op: ">=",
      hours: 500,
    },
    imagePrompt:
      "cyberpunk anime emperor in red and black robes with a penguin familiar at his side, throne of tux plushies, chibi half-body emblem, dark background, no text",
  },
  {
    id: "arch-btw",
    kind: "meme",
    label: "ARCH BTW",
    glyph: "🜁",
    description: "≥50h Linux AND terminal-editor share ≥90% — I use Arch, btw",
    rank: 120,
    condition: {
      kind: "all",
      of: [
        {
          kind: "axis-time",
          axis: "platforms",
          value: "linux",
          op: ">=",
          hours: 50,
        },
        {
          kind: "any",
          of: [
            { kind: "axis-pct", axis: "editors", value: "vim", op: ">=", pct: 0.9 },
            { kind: "axis-pct", axis: "editors", value: "neovim", op: ">=", pct: 0.9 },
            { kind: "axis-pct", axis: "editors", value: "emacs", op: ">=", pct: 0.9 },
          ],
        },
      ],
    },
    imagePrompt:
      "cyberpunk anime archwizard in blue-and-red robes casting a spell that spells out a triangular arch sigil in glowing runes, chibi half-body emblem, dark background, no text",
  },
  {
    id: "wsl-pilgrim",
    kind: "meme",
    label: "WSL PILGRIM",
    glyph: "⇌",
    description: "≥50h Windows AND ≥50h Linux — the pilgrim between two worlds",
    rank: 105,
    condition: {
      kind: "all",
      of: [
        {
          kind: "axis-time",
          axis: "platforms",
          value: "windows",
          op: ">=",
          hours: 50,
        },
        {
          kind: "axis-time",
          axis: "platforms",
          value: "linux",
          op: ">=",
          hours: 50,
        },
      ],
    },
    imagePrompt:
      "cyberpunk anime pilgrim with a windowed and a penguin-marked shoulder pauldron, walking between two neon gates, chibi half-body emblem, dark background, no text",
  },

  // -- § CATEGORY OP  (category-axis power fantasies) -----------------------
  {
    id: "prompt-engineer-supreme",
    kind: "meme",
    label: "PROMPT ENGINEER SUPREME",
    glyph: "✧",
    description: "AI-coding ≥50% — the supreme prompt whisperer",
    rank: 130,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "ai coding",
      op: ">=",
      pct: 0.5,
    },
    imagePrompt:
      "cyberpunk anime supreme prompt engineer with a floating ring of glowing red prompt scrolls orbiting their head, chibi half-body emblem, dark background, no text",
  },
  {
    id: "kubernetes-cultist",
    kind: "meme",
    label: "KUBERNETES CULTIST",
    glyph: "⎈",
    description: "Building-category ≥10% — the k8s cult accepts you",
    rank: 110,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "building",
      op: ">=",
      pct: 0.1,
    },
    imagePrompt:
      "cyberpunk anime cultist wearing a ship-wheel amulet, standing over a glowing red cluster of container pods, chibi half-body emblem, dark background, no text",
  },
  {
    id: "markdown-monk",
    kind: "meme",
    label: "MARKDOWN MONK",
    glyph: "❦",
    description: "Doc-writing ≥15% — the markdown monk speaks in headings",
    rank: 110,
    condition: {
      kind: "axis-pct",
      axis: "categories",
      value: "writing docs",
      op: ">=",
      pct: 0.15,
    },
    imagePrompt:
      "cyberpunk anime monk in flowing red-trimmed robes with a giant hashtag prayer-bead necklace, meditating over a scroll of documentation, chibi half-body emblem, dark background, no text",
  },
  {
    id: "regex-sorcerer",
    kind: "meme",
    label: "REGEX SORCERER",
    glyph: "∼",
    description:
      "Debugging ≥5% AND ≥100h in Rust or Go — the regex sorcerer walks the low-level path",
    rank: 115,
    condition: {
      kind: "all",
      of: [
        { kind: "axis-pct", axis: "categories", value: "debugging", op: ">=", pct: 0.05 },
        {
          kind: "any",
          of: [
            { kind: "axis-time", axis: "languages", value: "rust", op: ">=", hours: 100 },
            { kind: "axis-time", axis: "languages", value: "go", op: ">=", hours: 100 },
          ],
        },
      ],
    },
    imagePrompt:
      "cyberpunk anime sorcerer weaving glowing red regex patterns in the air, ancient scrolls floating around, chibi half-body emblem, dark background, no text",
  },
  {
    id: "commit-amender",
    kind: "meme",
    label: "COMMIT AMENDER",
    glyph: "↻",
    description: "Sprinter AND meeting ≥5% — the frantic amender never stops rewriting",
    rank: 105,
    condition: {
      kind: "all",
      of: [
        { kind: "trend", window: "last7-vs-prior7", op: ">=", ratio: 1.5 },
        { kind: "axis-pct", axis: "categories", value: "meeting", op: ">=", pct: 0.05 },
      ],
    },
    imagePrompt:
      "cyberpunk anime figure at a terminal frantically rewriting glowing red commit messages that spiral around them, chibi half-body emblem, dark background, no text",
  },

  // -- § META (composed labels — the S+ tier) --------------------------------
  {
    id: "true-grindset-s-plus",
    kind: "meme",
    label: "TRUE GRINDSET (S+)",
    glyph: "✦",
    description:
      "SIGMA GRINDSET + WEEKEND WARLORD + late-night ≥25% — the trinity of grindset",
    rank: 165,
    condition: {
      kind: "all",
      of: [
        // Composes the same conditions the sub-labels use — DRY-ing would
        // require self-referencing the catalog, which we deliberately don't
        // do (keeps evaluator a single pass). Keep in sync manually if the
        // sub-thresholds move.
        { kind: "daily-avg", op: ">=", hours: 6 },
        { kind: "punchcard-dow-pct", dowIn: [0, 6], op: ">=", pct: 0.5 },
        {
          kind: "punchcard-hour-pct",
          hoursIn: [22, 23, 0, 1, 2, 3],
          op: ">=",
          pct: 0.25,
        },
      ],
    },
    imagePrompt:
      "cyberpunk anime deity figure standing atop a mountain of servers, three glowing red halos overlapping behind their head, chibi half-body emblem, dark background, no text",
  },
  {
    id: "based-chad-ultimate",
    kind: "meme",
    label: "BASED CHAD ULTIMATE",
    glyph: "★",
    description:
      "CHAD DEVELOPER + IMPERIAL FIST + FOR THE EMPEROR — based, chad, and imperial",
    rank: 160,
    condition: {
      kind: "all",
      of: [
        { kind: "top-share", axis: "projects", op: ">=", pct: 0.8 },
        { kind: "streak", which: "current", op: ">=", days: 60 },
        { kind: "streak", which: "longest", op: ">=", days: 60 },
        { kind: "top-share", axis: "languages", op: ">=", pct: 0.9 },
      ],
    },
    imagePrompt:
      "cyberpunk anime ultimate chad in warhammer 40k power armor with red trim, standing in front of a giant glowing sigil, chibi half-body emblem, dark background, no text",
  },
];
