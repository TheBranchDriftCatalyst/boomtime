# Label Thematic-Criteria Synchronicity Audit

**Scope:** all 114 rows in `internal/db/migrations/00036_labels_catalog.sql`.
**Goal:** for each label, decide whether the **name + lore** cleanly matches the
**firing condition**. Read the header table for the quick scan; use the
per-cluster sections for the reasoning, and jump to the two recommendation
sections at the bottom for the actionable output.

**Verdict legend**

- **SYNC** — name + lore + condition all point at the same behavior; nothing to
  do.
- **LOOSE** — metaphor stretches but a reasonable reader will not feel lied to;
  leave it alone unless polishing.
- **MISMATCH** — the name or lore promises something that the criteria don't
  actually check for (e.g. `space-marine` says "24/7 codex-watch", criteria
  fire on "off-business-hours"). These are the rename/tweak targets.

---

## Cluster counts

| Cluster | Total | SYNC | LOOSE | MISMATCH |
|---|---:|---:|---:|---:|
| Tier (languages + editors) | 45 | 45 | 0 | 0 |
| Archetype | 14 | 11 | 2 | 1 |
| Tribe | 7 | 6 | 1 | 0 |
| Meme (pop-culture / warhammer / kawaii / -core) | 48 | 27 | 12 | 9 |
| **Total** | **114** | **89** | **15** | **10** |

The tier band is fully synced (they're all just `axis-time ≥ Nh` and the label
is literally `<AXIS> <TIER>`). Mismatches concentrate in the meme cluster,
where the name reaches for a specific pop-culture trope but the criteria pick
a generic threshold.

---

## Header table — id · glyph · label · criteria · verdict

### Tier — Languages (25 rows, all SYNC)

| id | glyph | label | criteria | verdict |
|---|---|---|---|---|
| languages-python-novice | 🐍 | PYTHON NOVICE | ≥ 5h in python (languages) | SYNC |
| languages-python-apprentice | 🐍 | PYTHON APPRENTICE | ≥ 20h in python | SYNC |
| languages-python-adept | 🐍 | PYTHON ADEPT | ≥ 100h in python | SYNC |
| languages-python-master | 🐍 | PYTHON MASTER | ≥ 500h in python | SYNC |
| languages-python-legend | 🐍 | PYTHON LEGEND | ≥ 2000h in python | SYNC |
| languages-typescript-novice | 🅃🅂 | TYPESCRIPT NOVICE | ≥ 5h in typescript | SYNC |
| languages-typescript-apprentice | 🅃🅂 | TYPESCRIPT APPRENTICE | ≥ 20h in typescript | SYNC |
| languages-typescript-adept | 🅃🅂 | TYPESCRIPT ADEPT | ≥ 100h in typescript | SYNC |
| languages-typescript-master | 🅃🅂 | TYPESCRIPT MASTER | ≥ 500h in typescript | SYNC |
| languages-typescript-legend | 🅃🅂 | TYPESCRIPT LEGEND | ≥ 2000h in typescript | SYNC |
| languages-javascript-novice | 🟨 | JAVASCRIPT NOVICE | ≥ 5h in javascript | SYNC |
| languages-javascript-apprentice | 🟨 | JAVASCRIPT APPRENTICE | ≥ 20h in javascript | SYNC |
| languages-javascript-adept | 🟨 | JAVASCRIPT ADEPT | ≥ 100h in javascript | SYNC |
| languages-javascript-master | 🟨 | JAVASCRIPT MASTER | ≥ 500h in javascript | SYNC |
| languages-javascript-legend | 🟨 | JAVASCRIPT LEGEND | ≥ 2000h in javascript | SYNC |
| languages-go-novice | 🐹 | GO NOVICE | ≥ 5h in go | SYNC |
| languages-go-apprentice | 🐹 | GO APPRENTICE | ≥ 20h in go | SYNC |
| languages-go-adept | 🐹 | GO ADEPT | ≥ 100h in go | SYNC |
| languages-go-master | 🐹 | GO MASTER | ≥ 500h in go | SYNC |
| languages-go-legend | 🐹 | GO LEGEND | ≥ 2000h in go | SYNC |
| languages-rust-novice | 🦀 | RUST NOVICE | ≥ 5h in rust | SYNC |
| languages-rust-apprentice | 🦀 | RUST APPRENTICE | ≥ 20h in rust | SYNC |
| languages-rust-adept | 🦀 | RUST ADEPT | ≥ 100h in rust | SYNC |
| languages-rust-master | 🦀 | RUST MASTER | ≥ 500h in rust | SYNC |
| languages-rust-legend | 🦀 | RUST LEGEND | ≥ 2000h in rust | SYNC |

**Notes.** The tier band is intentionally boring: label reads
`<AXIS> <TIER>`, criteria reads `axis-time on <axis> at hours ≥ threshold`,
lore reads "you have done this for a while". Nothing to triage.

### Tier — Editors (20 rows, all SYNC)

| id | glyph | label | criteria | verdict |
|---|---|---|---|---|
| editors-vim-novice | ▌ | VIM NOVICE | ≥ 5h in vim (editors) | SYNC |
| editors-vim-apprentice | ▌ | VIM APPRENTICE | ≥ 20h in vim | SYNC |
| editors-vim-adept | ▌ | VIM ADEPT | ≥ 100h in vim | SYNC |
| editors-vim-master | ▌ | VIM MASTER | ≥ 500h in vim | SYNC |
| editors-vim-legend | ▌ | VIM LEGEND | ≥ 2000h in vim | SYNC |
| editors-neovim-novice | ◆ | NEOVIM NOVICE | ≥ 5h in neovim | SYNC |
| editors-neovim-apprentice | ◆ | NEOVIM APPRENTICE | ≥ 20h in neovim | SYNC |
| editors-neovim-adept | ◆ | NEOVIM ADEPT | ≥ 100h in neovim | SYNC |
| editors-neovim-master | ◆ | NEOVIM MASTER | ≥ 500h in neovim | SYNC |
| editors-neovim-legend | ◆ | NEOVIM LEGEND | ≥ 2000h in neovim | SYNC |
| editors-vscode-novice | 🅥 | VSCODE NOVICE | ≥ 5h in vscode | SYNC |
| editors-vscode-apprentice | 🅥 | VSCODE APPRENTICE | ≥ 20h in vscode | SYNC |
| editors-vscode-adept | 🅥 | VSCODE ADEPT | ≥ 100h in vscode | SYNC |
| editors-vscode-master | 🅥 | VSCODE MASTER | ≥ 500h in vscode | SYNC |
| editors-vscode-legend | 🅥 | VSCODE LEGEND | ≥ 2000h in vscode | SYNC |
| editors-emacs-novice | 𝓔 | EMACS NOVICE | ≥ 5h in emacs | SYNC |
| editors-emacs-apprentice | 𝓔 | EMACS APPRENTICE | ≥ 20h in emacs | SYNC |
| editors-emacs-adept | 𝓔 | EMACS ADEPT | ≥ 100h in emacs | SYNC |
| editors-emacs-master | 𝓔 | EMACS MASTER | ≥ 500h in emacs | SYNC |
| editors-emacs-legend | 𝓔 | EMACS LEGEND | ≥ 2000h in emacs | SYNC |

### Archetype (14 rows)

| id | glyph | label | criteria | verdict |
|---|---|---|---|---|
| late-night-coder | 🌙 | LATE NIGHT CODER | ≥ 25% activity in hours {22,23,0,1,2,3} | SYNC |
| early-bird | 🌅 | EARLY BIRD | ≥ 25% activity in hours {5,6,7,8,9} | SYNC |
| weekend-warrior | ⚔ | WEEKEND WARRIOR | ≥ 30% activity on days {Sun, Sat} | SYNC |
| monogamist | 💍 | MONOGAMIST | top project share ≥ 70% | SYNC |
| polyglot | 🗣 | POLYGLOT | ≥ 5 distinct languages, each ≥ 5h | SYNC |
| consistent | 🔥 | CONSISTENT | current streak ≥ 30 days | SYNC |
| sprinter | 🚀 | SPRINTER | last-7d / prior-7d ratio ≥ 2 | SYNC |
| machine | 🤖 | MACHINE | daily-avg ≥ 3h | LOOSE (see notes) |
| deep-focus | 🎯 | DEEP FOCUS | top project share ≥ 80% | SYNC |
| multi-tasker | 🔀 | MULTI TASKER | ≥ 5 distinct projects, each ≥ 5h | SYNC |
| meeting-warrior | 📞 | MEETING WARRIOR | ≥ 10% of categories is 'meeting' | LOOSE (see notes) |
| ai-native | ✨ | AI NATIVE | ≥ 25% of categories is 'ai coding' | SYNC |
| test-obsessive | ✅ | TEST OBSESSIVE | ≥ 5% of categories is 'writing tests' | MISMATCH |
| documenter | 📝 | DOCUMENTER | ≥ 5% of categories is 'writing docs' | LOOSE |

**Archetype notes.**

- `machine` at daily-avg ≥ 3h is LOOSE: the lore literally says "three hours a
  day minimum" so the number is consistent, but "machine / tireless android"
  is a stretch for what's basically a daily habit. Fine as-is.
- `meeting-warrior` at ≥ 10% meetings is LOOSE — "warrior" implies dominance
  but 10% is more "regular participant". The lore ("meetings are combat, every
  yes-or-no answer is a sword stroke") reads okay because a real meeting
  warrior is defined by intensity per meeting, which the tracker can't see.
- `test-obsessive` at ≥ 5% writing-tests = MISMATCH. Five percent is not
  obsession, it's baseline hygiene. An obsessive should be 20-30%+ of tracked
  time in tests. Either rename to `TEST HYGIENIST` / `TEST DILIGENT` or raise
  the threshold. See rec block.
- `documenter` at ≥ 5% docs is LOOSE for the same reason but "documenter"
  scans as a description more than an intensifier, so it's fine.

### Tribe (7 rows)

| id | glyph | label | criteria | verdict |
|---|---|---|---|---|
| vim-enjoyer | ▌ | VIM ENJOYER | ≥ 10h in vim (editors) | SYNC |
| emacs-elder | 𝓔 | EMACS ELDER | ≥ 100h in emacs | SYNC |
| terminal-purist | ❯ | TERMINAL PURIST | ≥ 90% editors share in {vim OR neovim OR emacs} | LOOSE (see notes) |
| mac-native | 🍎 | MAC NATIVE | ≥ 200h on mac (platforms) | SYNC |
| linux-warlord | 🐧 | LINUX WARLORD | ≥ 200h on linux | SYNC |
| windows-survivor | 🪟 | WINDOWS SURVIVOR | ≥ 200h on windows | SYNC |
| cross-platform | 🔗 | CROSS PLATFORM | ≥ 2 distinct platforms, each ≥ 50h | SYNC |

**Tribe notes.** `terminal-purist` includes emacs, which is technically also
"in a terminal a lot of the time" but the meme "terminal purist" evokes
`vim`/`nvim`/`tmux` more than emacs (a GUI-first editor for many). LOOSE, fine
as-is — the lore ("if it doesn't fit in eighty columns it's not worth doing")
covers emacs terminal-mode devotees.

### Meme (48 rows)

| id | glyph | label | criteria | verdict |
|---|---|---|---|---|
| sigma-grindset | Σ | SIGMA GRINDSET | daily-avg ≥ 6h | SYNC |
| gigachad-committer | ◉ | GIGACHAD COMMITTER | current streak ≥ 28 days | SYNC |
| alpha-mogger | ▲ | ALPHA MOGGER | top project share ≥ 90% | SYNC |
| mewing-master | ◐ | MEWING MASTER | ≥ 30% activity in hours {5,6,7} | SYNC |
| hustle-android | ☗ | HUSTLE ANDROID | daily-avg ≥ 8h | SYNC |
| sleep-is-a-construct | ☾ | SLEEP-IS-A-CONSTRUCT | ≥ 40% activity in hours {0,1,2,3,4} | SYNC |
| weekend-warlord | ⚔ | WEEKEND WARLORD | ≥ 50% activity on days {Sun, Sat} | SYNC |
| **space-marine** | ☩ | **SPACE MARINE** | daily-avg ≥ 3h AND NOT (≥ 60% activity in hours 9-14) | **MISMATCH** |
| imperial-fist | ✊ | IMPERIAL FIST | longest streak ≥ 60 days | SYNC |
| battle-brother-of-the-keyboard | ⛨ | BATTLE-BROTHER OF THE KEYBOARD | longest streak ≥ 200 days | SYNC |
| for-the-emperor | ♆ | FOR THE EMPEROR | top language share ≥ 90% | SYNC |
| chapter-master | ☬ | CHAPTER MASTER | ≥ 1 project with ≥ 500h | SYNC |
| dreadnought-pilot | ◈ | DREADNOUGHT PILOT | daily-avg ≥ 3h AND longest streak ≥ 180 days | SYNC |
| lord-solar | ☀ | LORD SOLAR | daily-avg ≥ 12h | SYNC |
| **inquisitor** | ☒ | **INQUISITOR** | ≥ 8 distinct projects, each ≥ 5h | **MISMATCH** |
| kawaii-warlord | ❀ | KAWAII WARLORD | ≥ 15% of categories is 'designing' | LOOSE |
| tsundere-compiler | !? | TSUNDERE COMPILER | ≥ 100h in typescript | SYNC |
| yandere-debugger | ✂ | YANDERE DEBUGGER | ≥ 10% of categories is 'debugging' | LOOSE |
| catboy-operator | ≽^•⩊•^≼ | CATBOY OPERATOR | ≥ 50h vim AND ≥ 50h linux | SYNC |
| femboy-fortress | ♡ | FEMBOY FORTRESS | ≥ 500h on linux | SYNC |
| kawaii-code-mage | ✿ | KAWAII CODE MAGE | ≥ 100h python AND ≥ 20% 'ai coding' | SYNC |
| **maid-cafe-manager** | ☕ | **MAID CAFE MANAGER** | ≥ 10% meetings AND ≥ 5 languages each ≥ 5h | **MISMATCH** |
| commander-neko-paws | 🐾 | COMMANDER NEKO PAWS | daily-avg ≥ 6h AND current streak ≥ 60d AND top-project ≥ 70% | SYNC |
| poggers-committer | ◎ | POGGERS COMMITTER | last-7d/prior-7d ratio ≥ 2 | SYNC |
| omegalul-warlock | ≋ | OMEGALUL WARLOCK | top-project ≥ 80% AND ≥ 50% weekend activity | SYNC |
| copium-connoisseur | ☁ | COPIUM CONNOISSEUR | ≥ 35% 'ai coding' | LOOSE |
| malding-supreme | ≠ | MALDING SUPREME | ≥ 8 distinct projects, each ≥ 5h | LOOSE (see notes) |
| based-department | ◐ | BASED DEPARTMENT | ≥ 100h go OR ≥ 100h rust | SYNC |
| chad-developer | ♞ | CHAD DEVELOPER | top-project ≥ 80% AND current streak ≥ 60d | SYNC |
| sigma-male | σ | SIGMA MALE | top-project ≥ 95% | SYNC |
| **rizz-lord** | ♛ | **RIZZ LORD** | ≥ 25% 'ai coding' AND ≥ 10% 'designing' | **MISMATCH** |
| vim-bushido | 刀 | VIM BUSHIDO | ≥ 50h in vim | SYNC |
| emacs-overlord | 𝓔 | EMACS OVERLORD | ≥ 200h in emacs | SYNC |
| neovim-daimyo | ◆ | NEOVIM DAIMYO | ≥ 50h in neovim | SYNC |
| helix-prophet | ⌇ | HELIX PROPHET | ≥ 20h in helix | SYNC |
| **tmux-warlord** | ⧉ | **TMUX WARLORD** | ≥ 90% editors share in {vim/nvim/emacs/helix} | **MISMATCH** |
| **alacritty-devout** | ≡ | **ALACRITTY DEVOUT** | ≥ 100h vim OR ≥ 100h neovim | **MISMATCH** |
| mac-warlord | ⌘ | MAC WARLORD | ≥ 500h on mac | SYNC |
| linux-emperor | ♔ | LINUX EMPEROR | ≥ 500h on linux | SYNC |
| arch-btw | 🜁 | ARCH BTW | ≥ 50h linux AND ≥ 90% editor share terminal-editor | LOOSE (see notes) |
| wsl-pilgrim | ⇌ | WSL PILGRIM | ≥ 50h windows AND ≥ 50h linux | SYNC |
| prompt-engineer-supreme | ✧ | PROMPT ENGINEER SUPREME | ≥ 50% 'ai coding' | SYNC |
| **kubernetes-cultist** | ⎈ | **KUBERNETES CULTIST** | ≥ 10% of categories is 'building' | **MISMATCH** |
| markdown-monk | ❦ | MARKDOWN MONK | ≥ 15% 'writing docs' | SYNC |
| **regex-sorcerer** | ∼ | **REGEX SORCERER** | ≥ 5% debugging AND (≥ 100h rust OR ≥ 100h go) | **MISMATCH** |
| **commit-amender** | ↻ | **COMMIT AMENDER** | last-7d/prior-7d ratio ≥ 1.5 AND ≥ 5% meetings | **MISMATCH** |
| true-grindset-s-plus | ✦ | TRUE GRINDSET (S+) | daily-avg ≥ 6h AND ≥ 50% weekend AND ≥ 25% late-night | SYNC |
| based-chad-ultimate | ★ | BASED CHAD ULTIMATE | top-project ≥ 80% AND cur ≥ 60d AND longest ≥ 60d AND top-lang ≥ 90% | SYNC |

---

## Mismatch reasoning (the 10 that fail)

Each entry: what the name promises → what the criteria actually check → the
delta.

### 1. `space-marine` — "24/7 codex-watch" vs "off-business-hours grinder"

- **Promise (name + lore):** Warhammer 40k space marine, "twenty-four seven
  watch on the emperor's codebase" — evokes tireless around-the-clock service.
- **Criteria:** `daily-avg ≥ 3h AND NOT (≥ 60% activity in hours 9-14)` — the
  literal check is "puts in at least 3h/day AND does most work OUTSIDE the
  business-hours window". That is a **night-shift** / **graveyard-shift**
  archetype, not a 24/7 warrior.
- **Delta:** the anti-business-hours clause is the distinguishing feature; the
  name completely omits it. A "space marine" reading the lore would expect
  something like `daily-avg ≥ 3h AND longest-streak ≥ 30d`, not
  "avoids the day shift".

### 2. `inquisitor` — "audit / interrogate" vs "just juggling a lot of stuff"

- **Promise:** an inquisitor with a rosette, "eight projects each earning
  their audit is grounds for interrogation" — evokes scrutiny, forensic
  attention.
- **Criteria:** `≥ 8 distinct projects, each ≥ 5h` — the criteria are pure
  breadth, nothing about depth of investigation.
- **Delta:** this is literally the same criteria as `malding-supreme` (also 8
  projects × 5h) with a different metaphor. Both describe "spread thin", not
  "investigator". If we want a real inquisitor we'd need something like
  "≥ 10% debugging AND ≥ 5 distinct projects" (auditing multiple codebases).

### 3. `maid-cafe-manager` — "polyglot polymath" vs "language-hopper with
meetings"

- **Promise:** "serving a tray of glowing tokens to five little developer
  chibis" — the manager metaphor works for the "5 languages" part but the
  "meetings ≥ 10%" clause is where it snaps.
- **Criteria:** `≥ 10% meetings AND ≥ 5 languages × 5h`.
- **Delta:** it's a reasonable stack, but "maid cafe manager" doesn't scan as
  "person in meetings". A better metaphor for meetings + polyglot would be
  something like `SALON MADAME` / `TRANSLATOR DIPLOMAT`. Keeping the label but
  tweaking the lore to lean into "juggles conversations across languages" would
  also work — it's borderline. Called MISMATCH because the current lore doesn't
  make the meeting-part legible.

### 4. `rizz-lord` — "aesthetic rizz / drip" vs "AI + design categories"

- **Promise:** "sunglasses reflecting scrolling code, gold chain, silk shirt"
  — pure style/charisma vibe.
- **Criteria:** `≥ 25% 'ai coding' AND ≥ 10% 'designing'`.
- **Delta:** the "designing" half is defensible (aesthetics), but "≥ 25% AI"
  reads as "prompt-engineer-lite", not "rizz". If we're keeping the name, the
  criteria should lean harder into design + a low bar of "public-facing" work
  (docs, design, screenshots). Alternative: rename to `PROMPT & PIXEL` /
  `AI STYLIST`.

### 5. `tmux-warlord` — "multiplexer supremacy" vs "any terminal-editor at 90%"

- **Promise:** tmux — the multiplexer that lets you stack panes. Lore: "never
  leaves the multiplexer".
- **Criteria:** `≥ 90% editors share in any of {vim, neovim, emacs, helix}`.
  Not a single mention of tmux, terminal multiplexer, or panes. The criteria
  are identical to `terminal-purist` except with helix added.
- **Delta:** we have no tmux axis (Wakatime doesn't track it as an editor).
  Either rename to `TERMINAL SUPREME` / `MULTIPLEXER MONK` and lean into the
  "modal editor supremacy" story, or accept it's an alias for
  `terminal-purist` and delete one.

### 6. `alacritty-devout` — "Alacritty terminal emulator" vs "modal editor
usage"

- **Promise:** Alacritty is a specific GPU-accelerated terminal emulator. Lore:
  "kneeling before a glowing red terminal icon set on a stone altar."
- **Criteria:** `≥ 100h vim OR ≥ 100h neovim`. Zero connection to Alacritty
  specifically.
- **Delta:** same problem as tmux — no axis for terminal emulators. Rename to
  `MODAL DEVOUT` or `TERMINAL EDITOR DEVOUT`. Currently this is a modal-editor
  tier label wearing a terminal-emulator costume.

### 7. `kubernetes-cultist` — "k8s / container cult" vs "generic 'building'
category"

- **Promise:** k8s helm-wheel amulet, "ritual candles arranged as pods". Very
  specific to k8s / containers.
- **Criteria:** `≥ 10% of categories is 'building'`. The Wakatime `building`
  category is "compiling, running builds, etc." — has nothing to do with
  Kubernetes specifically.
- **Delta:** wildly generic. If we don't have a k8s/container axis, this
  should be renamed to `BUILD ENGINEER` / `BUILDCORE ARTISAN` / `COMPILE
  MONK`. As-is, someone who does zero k8s work but runs many
  `go build` / `cargo build` / `webpack build` sessions earns the "k8s cultist"
  badge, which is nonsense.

### 8. `regex-sorcerer` — "regex mastery" vs "debugging + Rust or Go"

- **Promise:** "weaving glowing regex patterns in the air ... they see the
  state machine behind the pattern." Very specific to regex.
- **Criteria:** `≥ 5% debugging AND (≥ 100h rust OR ≥ 100h go)`. No axis for
  regex use. The Rust/Go bias reads as "systems debugger" — but why is
  Python/JS excluded? Regex is arguably more used in JS/Python than in Go.
- **Delta:** rename to `STATE-MACHINE DEBUGGER` / `SYSTEMS DEBUGGER` /
  `LOW-LEVEL DETECTIVE` to match the criteria (debugging + systems languages),
  and free the `regex-sorcerer` name for a future language-neutral criteria
  built on... regex, if we ever add such an axis.

### 9. `commit-amender` — "git history rewriter" vs "sprinting with meetings"

- **Promise:** "frantically rewriting commit messages that spiral around them"
  — specifically about `git commit --amend` / rebase / history rewrite.
- **Criteria:** `last-7d/prior-7d ratio ≥ 1.5 AND ≥ 5% meetings`. No git
  activity, no commit-count, no rebase signal.
- **Delta:** the current criteria pattern-match to "picking up speed while
  managing meeting load" — that's a `SPRINT COORDINATOR` or `HYBRID
  OPERATOR`, not a commit-amender. The name promises something we can't
  actually measure with Wakatime axes.

### 10. `test-obsessive` (Archetype) — "obsession" vs "baseline hygiene"

- **Promise:** "forensic and patient ... every branch tested, every edge case
  named."
- **Criteria:** `≥ 5% of categories is 'writing tests'`. Five percent is not
  obsession; it's the floor for "yes I write tests occasionally".
- **Delta:** either rename to `TEST HYGIENIST` / `TEST-COVERAGE MEDIC` and
  keep the low bar, or keep the name and raise the threshold to ~25%. See
  criteria-tweak recs.

---

## Rename recommendations

For each mismatch we prefer **rename over criteria tweak** when the criteria
still describe a coherent behavior — that keeps existing awards attached to
the same row and just relabels them. (When the criteria are also broken, see
the criteria-tweak section.)

Because `id` is the PK and is referenced by user awards + label_images, we
keep the id stable and only change `label`, `glyph`, and `description`.

### 1. `space-marine` → **NIGHT WATCH**

```
id            : space-marine   (unchanged)
kind          : meme            (unchanged)
label         : NIGHT WATCH
glyph         : ☾
description   : A hooded cyber-sentinel walking the perimeter after the office
                lights die. Three hours a day minimum, mostly logged after the
                day-shift crowd has clocked out. The codebase is safest when
                the watch is standing.
optimized_prompt (redraft) :
  cyberpunk anime hooded night sentinel walking a neon perimeter, glowing red
  visor slit, cold moonlight from tall windows, empty office silhouettes in
  background, chibi half-body emblem, dark background
condition     : unchanged
```

Alternative names if you want to keep the warhammer flavor: `NIGHT LORDS` (a
40k chapter that literally operates at night), `MIDNIGHT SANCTIONED`.

### 2. `inquisitor` → **HERETIC HUNTER** (or rename + repoint criteria)

The criteria are pure breadth (`8 distinct projects × 5h`), which is also
`malding-supreme`. Two options:

- **Option A (cheap):** rename to align with breadth.
  ```
  label : SWARM OPERATIVE
  glyph : ✦
  description : Eight simultaneous projects, each with real hours logged. A
                cyber-operative whose focus fragments into a swarm of drones,
                each one running a different mission at once.
  ```
- **Option B (better, but criteria change too):** keep the inquisitor name and
  give it a real audit criteria. See criteria-tweak block #1.

### 3. `maid-cafe-manager` → keep name, tweak lore + swap meeting for
`punchcard-dow-pct` on weekdays

The "5 languages" half works with the "serves five developer chibis"
metaphor. The 10% meetings clause is what breaks the vibe. Two options:

- **Option A (rename only):**
  ```
  label : POLYGLOT DIPLOMAT
  glyph : 🗝
  description : A quiet-voiced diplomat with five language sigils floating at
                the fingertips, brokering commits between five different
                codebases. Ten percent of the day spent in the meeting room,
                the rest translating intent into syntax.
  ```
- **Option B (keep name, adjust lore):**
  ```
  label : MAID CAFE MANAGER  (unchanged)
  description : A kawaii anime maid manager with a red hair-ribbon serving
                trays of glowing tokens to a table of five little developer
                chibis, each speaking a different language sigil. She takes
                every meeting invite the counter throws at her — ten percent
                of her tracked time is table service.
  ```
  Option B keeps the meme name and tries to sell the meetings-clause. Softer
  rewrite; still LOOSE afterward.

### 4. `rizz-lord` → **AI STYLIST**

```
label : AI STYLIST
glyph : ♛
description : A cyberpunk anime stylist working in the intersection of prompt
              and pixel. A quarter of the tracked time in dialogue with the
              model, another slice in design tooling. The output looks
              effortless because the taste is not.
```

Alternative that keeps the rizz meme: `AI DRIP LORD` (keep glyph ♛), but
still tweak the description to explicitly call out prompt + design.

### 5. `tmux-warlord` → **MULTIPLEXER MONK** (or delete as duplicate of
`terminal-purist`)

Comparing the two:

| id | criteria |
|---|---|
| terminal-purist | `axis-pct editors ≥ 0.9` for vim OR neovim OR emacs |
| tmux-warlord   | same, plus helix |

Options:

- **Option A (rename to reflect what it measures):**
  ```
  label : MULTIPLEXER MONK
  glyph : ⧉
  description : A cyber-monk on a throne assembled from stacked terminal
                panes, red glow bleeding between the splits. Ninety percent of
                editor time in a modal terminal editor — the multiplexer is
                the temple.
  ```
- **Option B:** delete `tmux-warlord` and add helix to `terminal-purist`.
  Cleaner but loses a meme row.

### 6. `alacritty-devout` → **MODAL DEVOUT**

```
label : MODAL DEVOUT
glyph : ≡
description : A cyberpunk anime devotee kneeling before a glowing red modal
              editor icon on a stone altar. A hundred hours in vim or neovim.
              Every keystroke a small prayer, every motion a small vow.
```

If we ever add a per-terminal-emulator axis, the `alacritty-devout` id can be
repurposed — but as-is the criteria don't touch Alacritty at all.

### 7. `kubernetes-cultist` → **BUILD ENGINEER**

```
label : BUILD ENGINEER
glyph : ⚙
description : A cyberpunk anime engineer at a scaffold of glowing red build
              pipes, ten percent of every day spent watching bars fill and
              green checks light up. Compile, package, ship, repeat.
```

If we want to preserve the k8s meme for a future criteria that actually
touches container work: **rename this row to build-engineer** and free the
`kubernetes-cultist` id for later.

### 8. `regex-sorcerer` → **SYSTEMS DETECTIVE**

```
label : SYSTEMS DETECTIVE
glyph : 🔍
description : A cyberpunk anime detective bent over a red-glowing stack
              trace, floating hex dumps orbiting the head. Five percent of
              tracked time in debugging plus a hundred hours in Rust or Go.
              The bug does not survive the deposition.
```

If we ever get a "grep/regex" axis, the `regex-sorcerer` name is reusable —
right now it's misleading.

### 9. `commit-amender` → **SPRINT COORDINATOR**

```
label : SPRINT COORDINATOR
glyph : ↻
description : A cyberpunk anime coordinator on a treadmill of accelerating
              red timelines, one earpiece live, one hand still on the
              keyboard. Last week's velocity was fifty percent above the week
              before, and five percent of the day still lands in the meeting
              room. Momentum with meetings, meetings with momentum.
```

The `commit-amender` name is evocative but we can't actually measure
`git commit --amend` — save this name for the day we can (git-diff-based
axis).

### 10. `test-obsessive` → **TEST HYGIENIST** (Archetype, keep low threshold)

```
label : TEST HYGIENIST
glyph : ✅
description : A cyberpunk anime coder in a lab coat, forceps in one hand,
              magnifier in the other, red under-lighting on rows of neat green
              checks. Five percent of tracked time in tests — habit, not
              theater.
```

Alternative if you'd rather **keep the name** and adjust the criteria: see
criteria-tweak block #2.

---

## Criteria-tweak recommendations

For the cases where the **name is genuinely good** and we'd rather adjust the
criteria to match, here are the JSONB shapes.

### 1. `inquisitor` (keep name, actually measure audit behavior)

Current:
```json
{"kind":"distinct-count","axis":"projects","minHoursEach":5,"op":">=","n":8}
```

Proposed:
```json
{"kind":"all","of":[
  {"kind":"axis-pct","axis":"categories","value":"code reviewing","op":">=","pct":0.1},
  {"kind":"distinct-count","axis":"projects","minHoursEach":5,"op":">=","n":5}
]}
```

Reads as: "at least 10% of tracked time in code review AND touches at least 5
different projects at 5h+ each" — i.e. this person audits across multiple
codebases. Requires that `code reviewing` is an actual Wakatime category
string in the current dataset (verify against `categories` axis usage
elsewhere in the app; some Wakatime datasets use `Code Reviewing` cased
differently).

### 2. `test-obsessive` (keep name, raise threshold)

Current:
```json
{"kind":"axis-pct","axis":"categories","value":"writing tests","op":">=","pct":0.05}
```

Proposed (obsession = a quarter of tracked time):
```json
{"kind":"axis-pct","axis":"categories","value":"writing tests","op":">=","pct":0.25}
```

If you also want to keep `test-hygienist` as a lower-tier version, add a new
row (via admin CRUD, per the migration comment) with the 5% threshold.

### 3. `space-marine` (keep name if you like the 40k flavor, refile criteria)

If we want the name to actually mean "24/7 codex-watch", drop the anti-9-to-5
clause and lean into `daily-avg + long streak`:

Current:
```json
{"kind":"all","of":[
  {"kind":"daily-avg","op":">=","hours":3},
  {"kind":"not","of":{"kind":"punchcard-hour-pct","hoursIn":[9,10,11,12,13,14],"op":">=","pct":0.6}}
]}
```

Proposed:
```json
{"kind":"all","of":[
  {"kind":"daily-avg","op":">=","hours":3},
  {"kind":"streak","which":"longest","op":">=","days":30}
]}
```

(This does start to overlap with `dreadnought-pilot`, which is 3h/day + 180d
longest — space marine would be the "starter tier" of the same pattern.)
If you'd rather keep the anti-business-hours signal, the rename to
`NIGHT WATCH` (rec block #1) is the cleaner move.

### 4. `kubernetes-cultist` (keep name, need real signal)

There is no `building=='kubernetes'` axis. Practical options:

- **A.** Add an `axis-time` on a `tools` axis with value `kubectl` /
  `docker` / `helm` if the Wakatime plugin catches those. Verify by grep
  through the ingest layer; if not present, this rename is required.
- **B.** If we can enrich the dataset with a `tools` axis that includes
  `docker`/`kubernetes`/`helm`, the criteria becomes:
  ```json
  {"kind":"axis-time-sum","axis":"tools","values":["kubectl","helm","docker"],"op":">=","hours":50}
  ```
  Until such an axis exists, `kubernetes-cultist` should be renamed to
  `BUILD ENGINEER` (rename rec block #7).

### 5. `commit-amender` (keep name, need real signal)

Same story as kubernetes-cultist. There is no git-commit or git-amend axis.
Practical options:

- **A.** Add a new axis `git.action` with values like `commit`, `amend`,
  `rebase`, `merge`, then criteria:
  ```json
  {"kind":"axis-pct","axis":"git.action","value":"amend","op":">=","pct":0.1}
  ```
- **B.** Until that axis exists, rename to `SPRINT COORDINATOR` (rename rec
  block #9).

### 6. `regex-sorcerer` (keep name, need real signal)

There is no regex-usage axis. Options:

- **A.** If we ever add a `tools`/`workflow` axis with values like `grep`,
  `ripgrep`, `sed`, `awk`:
  ```json
  {"kind":"axis-time-sum","axis":"tools","values":["ripgrep","grep","sed","awk"],"op":">=","hours":25}
  ```
- **B.** Until then, rename to `SYSTEMS DETECTIVE` (rename rec block #8).

---

## Summary of proposed row changes

If we act on every recommendation above, the diff is:

| id | change | rationale |
|---|---|---|
| space-marine | rename → `NIGHT WATCH` (☾) + rewrite desc | criteria fire on off-hours, not 24/7 |
| inquisitor | keep name, tweak criteria to `10% code-review + 5+ projects` | current criteria are pure breadth |
| maid-cafe-manager | rewrite desc to sell the meeting-clause (or rename → `POLYGLOT DIPLOMAT`) | current desc doesn't cover meetings |
| rizz-lord | rename → `AI STYLIST` + rewrite desc | "rizz" doesn't map to AI+design |
| tmux-warlord | rename → `MULTIPLEXER MONK` (or delete as dup of terminal-purist) | criteria never touch tmux |
| alacritty-devout | rename → `MODAL DEVOUT` | criteria are modal-editor tier, not terminal emulator |
| kubernetes-cultist | rename → `BUILD ENGINEER` (until a real k8s axis exists) | criteria are generic `building` |
| regex-sorcerer | rename → `SYSTEMS DETECTIVE` (until regex axis exists) | criteria are debugging+systems langs |
| commit-amender | rename → `SPRINT COORDINATOR` (until git-action axis exists) | criteria don't touch git activity |
| test-obsessive | rename → `TEST HYGIENIST`, OR keep + raise threshold to 25% | 5% is not obsession |

**Non-recommendations (deliberately left LOOSE):**

- `machine`, `meeting-warrior`, `documenter`, `terminal-purist`,
  `kawaii-warlord`, `yandere-debugger`, `copium-connoisseur`,
  `malding-supreme`, `arch-btw` — the metaphor stretches but a reasonable
  reader will not feel the label is dishonest about what earned it.
- `chapter-master` — "500h in a single project = a chapter you have founded" is
  a good metaphor for `distinct-count with n=1 minHoursEach=500`, so left as
  SYNC.
- `for-the-emperor` at top-language ≥ 90% — "devotion, not diversity" reads
  cleanly.

**Non-content notes:**

- All rank values, tier band assignments, glyphs, and the systemPrompt in
  `label_gen_config` are out of scope for this audit — we only touch label /
  description / (occasionally) criteria.
- The `optimized_prompt` field for each renamed row will need a redraft to
  match the new label; the redrafts above are seed material, not final
  copy — the intent is to keep the crimson/black cyberpunk anime chibi
  aesthetic consistent with the rest of the catalog.
- `id` values MUST NOT change on any of these. User awards and
  `label_images` rows FK back to `id`. Only `label`, `glyph`, `description`,
  `optimized_prompt`, and (for the criteria-tweak recs) `condition` should be
  edited. Do the edits via the admin CRUD UI, not a new migration —
  migration 00036 is frozen by the "Once merged, this migration is frozen"
  policy in the file header.
