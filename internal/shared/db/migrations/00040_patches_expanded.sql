-- +goose Up
-- +goose StatementBegin

-- Memewarfare protocols wave 2 (boom-mwp / boom-0dw): expand the `patch`
-- catalog from 6 seed awards to 30 total.
--
-- ADDITIVE — the 114 existing labels stay put, the 6 seed patches from
-- 00039_patches.sql stay put. This migration adds 24 more patches
-- alongside them. No evaluator changes (types.ts / conditions.ts
-- untouched) — every condition below reuses primitives that already
-- exist. If you can't express a patch with what's there, drop the patch;
-- don't invent a new primitive.
--
-- Layout by evaluator combo (no two patches share a condition shape):
--
--   Time-of-day patches (punchcard-hour-pct)
--     NIGHT WATCH        22:00-02:00 ≥25%  + daily-avg ≥ 4h
--     REVEILLE           05:00-07:00 ≥15%  + current streak ≥ 14
--     GRAVEYARD SHIFT    00:00-03:00 ≥20%
--     ZERO DARK THIRTY   02:00-04:00 ≥12%  (extreme late)
--
--   Day-of-week patches (punchcard-dow-pct)
--     WEEKEND OP         Sat+Sun ≥30%
--
--   Volume / cadence
--     MARATHON RUNNER    daily-avg ≥ 6h
--     SPRINTER PATCH     trend ≥ 3.0 (huge spike vs prior week)
--     IRONMAN            longest streak ≥ 90
--     IRON WILL          current streak ≥ 60
--     CQC                daily-avg ≥ 3h AND current streak ≥ 7 (frequent + steady)
--     CENTURION          daily-avg ≥ 3h AND top-lang ≥ 50% AND longest streak ≥ 30
--
--   Focus (top-share)
--     SNIPER             top project share ≥ 80%
--     SPECIALIST         top language share ≥ 90%
--
--   Breadth (distinct-count)
--     OVERWATCH          3+ projects with ≥ 20h each
--     SCOUT              3+ editors with ≥ 5h each
--     JOINT OPS          3+ platforms with ≥ 10h each
--     POLYGLOT COMMANDER 5+ languages with ≥ 20h each (rarer than the
--                        POLYGLOT archetype which is 5h each)
--
--   Category mix (axis-pct on categories)
--     CARTOGRAPHER       "writing docs" ≥ 10%
--     LEGAL              "debugging" ≥ 15%
--     MED-EVAC           "meeting" ≥ 25%   (higher bar than SIGNAL CORPS)
--     INTEL              "designing" ≥ 10%
--     AI HANDLER         "ai coding" ≥ 15%
--
--   Tool loyalty (composed)
--     VIM RANGER         vim ≥ 100h AND vim is top editor ≥ 70%
--     RECON              (in wave 1)
--     IDE COMMANDO       any(vscode|intellij|pycharm ≥ 100h)
--
-- Condition field naming: matches TS types.ts + the correct 00036_labels_
-- catalog.sql precedent — distinct-count uses `minHoursEach`+`n`, streak
-- uses `which`, trend uses `window`. (The wave-1 seed migration used
-- `min_hours`+`count` which is a bug on the seeded RECON patch — leaving
-- that alone per handoff, patches wave-2 do it right.)

INSERT INTO public.labels
  (id, kind, label, glyph, description, optimized_prompt, rank, tier, condition)
VALUES

-- ── NIGHT WATCH ──────────────────────────────────────────────────────
('night-watch', 'patch', 'NIGHT WATCH', '🌙',
 'The tower is manned. ≥25% of your activity between 22:00-02:00 with a sustained 4h daily average — while the rest of the FOB sleeps, you hold the perimeter.',
 'cyberpunk-anime chibi sentry in tactical night-ops gear, glowing crimson nightvision monocle, one signature prop of a stopped digital clock reading 02:47, moonlit blue backlight cut by deep crimson rim light on jet black, NIGHT WATCH patch on shoulder, no text',
 235, '',
 '{"kind":"all","of":[
     {"kind":"punchcard-hour-pct","hoursIn":[22,23,0,1,2],"op":">=","pct":0.25},
     {"kind":"daily-avg","op":">=","hours":4}
   ]}'),

-- ── REVEILLE ─────────────────────────────────────────────────────────
('reveille', 'patch', 'REVEILLE', '🎺',
 'First light, boots on the deck. ≥15% of activity in the 05:00-07:00 window with a 14-day active streak — you sound the bugle before the mess hall opens.',
 'cyberpunk-anime chibi bugler in dawn-patrol tactical uniform, brass-and-chrome bugle raised to lips, single signature prop of a coffee thermos on the sling, warm amber sunrise motif behind deep crimson rim light on jet black, REVEILLE patch on shoulder, no text',
 228, '',
 '{"kind":"all","of":[
     {"kind":"punchcard-hour-pct","hoursIn":[5,6,7],"op":">=","pct":0.15},
     {"kind":"streak","which":"current","op":">=","days":14}
   ]}'),

-- ── WEEKEND OP ───────────────────────────────────────────────────────
('weekend-op', 'patch', 'WEEKEND OP', '📅',
 'Nobody works Saturday. You do. ≥30% of the punchcard lands on Sat+Sun — the two-day mission window where the base is quiet and the codebase is yours.',
 'cyberpunk-anime chibi operator in weekend-tactical loadout hoodie over armor plate, single signature prop of a paper wall-calendar with SAT+SUN circled in red, low-key fluorescent office backlight cut by deep crimson rim light on jet black, WEEKEND OP patch on shoulder, no text',
 220, '',
 '{"kind":"punchcard-dow-pct","dowIn":[0,6],"op":">=","pct":0.30}'),

-- ── GRAVEYARD SHIFT ──────────────────────────────────────────────────
('graveyard-shift', 'patch', 'GRAVEYARD SHIFT', '🕯',
 'The graveyard is a workplace. ≥20% of the punchcard falls between 00:00-03:00 — you know which server-room fluorescents flicker and which vending machine still takes cash.',
 'cyberpunk-anime chibi janitor-operator in coverall over tactical vest, single signature prop of a single flickering candle in a data-center rack aisle, cold cyan emergency lighting behind deep crimson rim light on jet black, GRAVEYARD SHIFT patch on shoulder, no text',
 240, '',
 '{"kind":"punchcard-hour-pct","hoursIn":[0,1,2,3],"op":">=","pct":0.20}'),

-- ── ZERO DARK THIRTY ─────────────────────────────────────────────────
('zero-dark-thirty', 'patch', 'ZERO DARK THIRTY', '🌑',
 'Extraction at oh-two-hundred. ≥12% of your keystrokes land between 02:00-04:00 — the pocket of the night no honest person is awake in.',
 'cyberpunk-anime chibi ghost-recon operator in matte-black tactical suit and balaclava, single signature prop of a suppressed keyboard with a red LED capslock, near-total blackout with only deep crimson rim light on jet black, ZERO DARK THIRTY patch on shoulder, no text',
 250, '',
 '{"kind":"punchcard-hour-pct","hoursIn":[2,3,4],"op":">=","pct":0.12}'),

-- ── MARATHON RUNNER ──────────────────────────────────────────────────
('marathon-runner', 'patch', 'MARATHON RUNNER', '🏃',
 'The distance is the point. Sustained ≥6h daily average — you do not sprint, you do not stop, you simply do not run out of road.',
 'cyberpunk-anime chibi endurance runner in lightweight tactical trainers, race-bib overlaid on plate carrier, single signature prop of a hydration bladder hose, long-exposure motion-blur trail behind deep crimson rim light on jet black, MARATHON RUNNER patch on shoulder, no text',
 232, '',
 '{"kind":"daily-avg","op":">=","hours":6}'),

-- ── SPRINTER PATCH ───────────────────────────────────────────────────
-- Distinct from the SPRINTER archetype (2x): this one demands a full 3x
-- explosion vs the prior week — a genuine incident-response spike, not a
-- normal week's momentum.
('sprinter-patch', 'patch', 'SPRINTER PATCH', '💨',
 'The velocity trace goes vertical. Last-7-days average ≥ 3× the prior 7 — this is not momentum, this is a full breach-and-clear.',
 'cyberpunk-anime chibi sprinter mid-stride in tactical carbon-plate racing armor, single signature prop of a shattered stopwatch trailing behind, streaked motion-blur speedlines lit by deep crimson rim light on jet black, SPRINTER PATCH patch on shoulder, no text',
 226, '',
 '{"kind":"trend","window":"last7-vs-prior7","op":">=","ratio":3.0}'),

-- ── IRONMAN ──────────────────────────────────────────────────────────
('ironman', 'patch', 'IRONMAN', '⛓',
 'Ninety consecutive days somewhere in your history. Longest streak ≥ 90 — the record itself is the medal; nothing broke you for a full quarter.',
 'cyberpunk-anime chibi operator in weathered iron-plate armor over tactical fatigues, single signature prop of a heavy iron chain wrapped around one gauntlet, forge-glow amber undertone behind deep crimson rim light on jet black, IRONMAN patch on shoulder, no text',
 252, '',
 '{"kind":"streak","which":"longest","op":">=","days":90}'),

-- ── IRON WILL ────────────────────────────────────────────────────────
-- Current streak (still active, still burning) is harder to hold than a
-- historical one — so this outranks IRONMAN when both fire? No: keep it
-- one tick lower so IRONMAN wins in tagline. Both firing = the vest is
-- decorated, both are worn.
('iron-will', 'patch', 'IRON WILL', '⚔',
 'The chain still hasn''t broken. Current streak ≥ 60 — active, ongoing, every dawn another link forged.',
 'cyberpunk-anime chibi vanguard in polished black-steel tactical armor, single signature prop of a red-hot forge-tongs still glowing with the fresh link, sparks arcing overhead cut by deep crimson rim light on jet black, IRON WILL patch on shoulder, no text',
 248, '',
 '{"kind":"streak","which":"current","op":">=","days":60}'),

-- ── SNIPER ───────────────────────────────────────────────────────────
('sniper', 'patch', 'SNIPER', '🎯',
 'One rifle, one project, one shot. Top-project share ≥ 80% — you don''t spread fire, you hold overwatch on the objective.',
 'cyberpunk-anime chibi marksman prone on a ghillie mat, single signature prop of a scoped rifle with a crimson holo-reticle floating in front of the eye, kneeling silhouette behind deep crimson rim light on jet black, SNIPER patch on shoulder, no text',
 230, '',
 '{"kind":"top-share","axis":"projects","op":">=","pct":0.80}'),

-- ── OVERWATCH ────────────────────────────────────────────────────────
('overwatch', 'patch', 'OVERWATCH', '🦅',
 'Three fronts, all covered deep. 3+ projects each holding ≥ 20h — you don''t just skim across repos, you actually work them.',
 'cyberpunk-anime chibi overwatch spotter in high-vantage tactical perch, single signature prop of three floating holo-scopes each tagged with a project waypoint, elevated silhouette behind deep crimson rim light on jet black, OVERWATCH patch on shoulder, no text',
 224, '',
 '{"kind":"distinct-count","axis":"projects","minHoursEach":20,"op":">=","n":3}'),

-- ── SPECIALIST ───────────────────────────────────────────────────────
('specialist', 'patch', 'SPECIALIST', '🎖',
 'One weapon, mastered. Top language share ≥ 90% — everyone else is a generalist; you are the one they call for THIS.',
 'cyberpunk-anime chibi weapons-specialist kneeling beside a single ornate rifle, single signature prop of a language-sigil enameled on the receiver, workshop-warm amber undertone cut by deep crimson rim light on jet black, SPECIALIST patch on shoulder, no text',
 236, '',
 '{"kind":"top-share","axis":"languages","op":">=","pct":0.90}'),

-- ── SCOUT ────────────────────────────────────────────────────────────
('scout', 'patch', 'SCOUT', '🧭',
 'You know the terrain because you walked it. 3+ editors each earning ≥ 5h — you don''t bind yourself to one tool, you scout ahead in whatever the mission needs.',
 'cyberpunk-anime chibi scout in lightweight recon fatigues, single signature prop of a spinning brass compass with three glowing arrows, tree-line silhouette behind deep crimson rim light on jet black, SCOUT patch on shoulder, no text',
 216, '',
 '{"kind":"distinct-count","axis":"editors","minHoursEach":5,"op":">=","n":3}'),

-- ── JOINT OPS ────────────────────────────────────────────────────────
('joint-ops', 'patch', 'JOINT OPS', '🤝',
 'Cross-platform coalition. 3+ platforms each holding ≥ 10h — Linux, Mac, Windows; the mission does not care which chassis, and neither do you.',
 'cyberpunk-anime chibi liaison officer in inter-service dress uniform over plate carrier, single signature prop of three shoulder-patch flags stitched down one arm, unified-command room backlight cut by deep crimson rim light on jet black, JOINT OPS patch on shoulder, no text',
 222, '',
 '{"kind":"distinct-count","axis":"platforms","minHoursEach":10,"op":">=","n":3}'),

-- ── POLYGLOT COMMANDER ───────────────────────────────────────────────
-- Harder bar than the POLYGLOT archetype (which is 5h/lang, 5 langs).
-- 20h each × 5 langs is a real polyglot operator, not a dabbler.
('polyglot-commander', 'patch', 'POLYGLOT COMMANDER', '🗺',
 'Five tongues, all fluent. 5+ languages each holding ≥ 20h — you don''t just recognize the syntax, you command in it.',
 'cyberpunk-anime chibi field-commander in decorated tactical dress with five language-sigil pins in a row on the chest, single signature prop of a folded ops-map bristling with five colored flags, HQ tent-light amber cut by deep crimson rim light on jet black, POLYGLOT COMMANDER patch on shoulder, no text',
 246, '',
 '{"kind":"distinct-count","axis":"languages","minHoursEach":20,"op":">=","n":5}'),

-- ── CARTOGRAPHER ─────────────────────────────────────────────────────
('cartographer', 'patch', 'CARTOGRAPHER', '📜',
 'The docs get written because you write them. ≥10% of activity in the writing-docs category — the terrain map exists because someone drew it.',
 'cyberpunk-anime chibi cartographer in field-scribe tactical smock, single signature prop of an unrolled parchment map pinned by a tactical knife, drafting-lamp amber pool cut by deep crimson rim light on jet black, CARTOGRAPHER patch on shoulder, no text',
 210, '',
 '{"kind":"axis-pct","axis":"categories","value":"writing docs","op":">=","pct":0.10}'),

-- ── LEGAL ────────────────────────────────────────────────────────────
-- Debugging = forensic work. Reading logs, tracing chain-of-custody
-- on a bug. Military "Legal" = JAG / investigator — same energy.
('legal', 'patch', 'LEGAL', '⚖',
 'Every incident gets an investigator. ≥15% of activity in the debugging category — you follow chain-of-custody on every bug until the case closes.',
 'cyberpunk-anime chibi investigator in JAG-tactical dress uniform, single signature prop of a red evidence-tag zip-tied to a floating stack-trace, courtroom-oak wood undertone cut by deep crimson rim light on jet black, LEGAL patch on shoulder, no text',
 212, '',
 '{"kind":"axis-pct","axis":"categories","value":"debugging","op":">=","pct":0.15}'),

-- ── MED-EVAC ─────────────────────────────────────────────────────────
-- Distinct from SIGNAL CORPS: SIGNAL CORPS = 15% meeting (comms-heavy).
-- MED-EVAC = 25% meeting — the "you are pulling people out of trouble
-- for a living" tier. Rare, high-signal.
('med-evac', 'patch', 'MED-EVAC', '🚁',
 'You extract people from trouble for a living. ≥25% of activity in the meeting category — this is not comms, this is field triage.',
 'cyberpunk-anime chibi flight-medic in red-cross-marked tactical flight suit, single signature prop of a rotor-blade shadow spinning overhead on the ground, dust-cloud amber wash cut by deep crimson rim light on jet black, MED-EVAC patch on shoulder, no text',
 208, '',
 '{"kind":"axis-pct","axis":"categories","value":"meeting","op":">=","pct":0.25}'),

-- ── INTEL ────────────────────────────────────────────────────────────
('intel', 'patch', 'INTEL', '🕵',
 'The plan gets drawn before the strike. ≥10% of activity in the designing category — you don''t open the editor until the wall is covered in red string.',
 'cyberpunk-anime chibi intel-analyst in trenchcoat over tactical undersuit, single signature prop of a corkboard-fragment crossed with red string and pinned polaroids, back-office fluorescent green cut by deep crimson rim light on jet black, INTEL patch on shoulder, no text',
 214, '',
 '{"kind":"axis-pct","axis":"categories","value":"designing","op":">=","pct":0.10}'),

-- ── AI HANDLER ───────────────────────────────────────────────────────
('ai-handler', 'patch', 'AI HANDLER', '🤖',
 'The drones fly because you fly them. ≥15% of activity in the ai-coding category — you are the operator, the model is the airframe.',
 'cyberpunk-anime chibi drone-handler in UAV-controller tactical vest, single signature prop of a small hovering red-eyed drone perched on the wrist, control-tower blue backlight cut by deep crimson rim light on jet black, AI HANDLER patch on shoulder, no text',
 218, '',
 '{"kind":"axis-pct","axis":"categories","value":"ai coding","op":">=","pct":0.15}'),

-- ── VIM RANGER ───────────────────────────────────────────────────────
-- Distinct from the vim-enjoyer tribe (which is just "you use vim at
-- all"): this one demands ≥100h in vim AND vim is your top editor with
-- ≥70% share — actual loyalty, not just presence.
('vim-ranger', 'patch', 'VIM RANGER', '🗡',
 'Modal blade, always drawn. ≥100h in vim AND vim is your top editor ≥ 70% — you do not just use it, you live inside the buffer.',
 'cyberpunk-anime chibi ranger in stealth-ranger tactical hood, single signature prop of a curved kukri blade etched with the mode-indicators NORMAL INSERT VISUAL, forest-edge green undertone cut by deep crimson rim light on jet black, VIM RANGER patch on shoulder, no text',
 234, '',
 '{"kind":"all","of":[
     {"kind":"axis-time","axis":"editors","value":"vim","op":">=","hours":100},
     {"kind":"top-share","axis":"editors","op":">=","pct":0.70}
   ]}'),

-- ── CENTURION ────────────────────────────────────────────────────────
-- Compound: sustained pace + language loyalty + real streak. The classic
-- "steady legionary" profile.
('centurion', 'patch', 'CENTURION', '🛡',
 'The line holds because you hold it. Daily avg ≥ 3h AND top language ≥ 50% share AND longest streak ≥ 30 — the legionary profile, three virtues at once.',
 'cyberpunk-anime chibi centurion in lorica-plate tactical armor with red horsehair crest on helm, single signature prop of a rectangular scutum shield lowered braced on the ground, phalanx-shadow silhouette behind deep crimson rim light on jet black, CENTURION patch on shoulder, no text',
 238, '',
 '{"kind":"all","of":[
     {"kind":"daily-avg","op":">=","hours":3},
     {"kind":"top-share","axis":"languages","op":">=","pct":0.50},
     {"kind":"streak","which":"longest","op":">=","days":30}
   ]}'),

-- ── IDE COMMANDO ─────────────────────────────────────────────────────
-- Heavy on any GUI IDE. The counterpart to VIM RANGER — different tool
-- loyalty, same "you racked up serious time in one class of tool".
('ide-commando', 'patch', 'IDE COMMANDO', '💻',
 'The full-loadout kit — refactor engine, debugger, integrated terminal, ≥100h in any of the big GUIs (vscode / intellij / pycharm). Heavy weapons class.',
 'cyberpunk-anime chibi commando in full-integrated exoskeleton tactical rig with wrist-mounted panels, single signature prop of a floating debugger call-stack projected from the forearm, workshop-fluorescent white cut by deep crimson rim light on jet black, IDE COMMANDO patch on shoulder, no text',
 206, '',
 '{"kind":"any","of":[
     {"kind":"axis-time","axis":"editors","value":"vscode","op":">=","hours":100},
     {"kind":"axis-time","axis":"editors","value":"intellij","op":">=","hours":100},
     {"kind":"axis-time","axis":"editors","value":"pycharm","op":">=","hours":100}
   ]}'),

-- ── CQC ──────────────────────────────────────────────────────────────
-- Close-quarters cadence: high frequency + short streak. The "always
-- swinging" profile — daily average ≥ 3h AND current streak ≥ 7. You
-- show up every day and you show up loud.
('cqc', 'patch', 'CQC', '🔪',
 'Close-quarters cadence. Daily avg ≥ 3h AND current streak ≥ 7 — no lulls, no breaks, always on the balls of your feet.',
 'cyberpunk-anime chibi close-quarters operator crouched forward, single signature prop of a matte-black karambit knife held in reverse-grip, tight-corridor red-emergency wash cut by deep crimson rim light on jet black, CQC patch on shoulder, no text',
 204, '',
 '{"kind":"all","of":[
     {"kind":"daily-avg","op":">=","hours":3},
     {"kind":"streak","which":"current","op":">=","days":7}
   ]}');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Wave 2 rollback — remove ONLY the 24 patches added by this migration.
-- The wave-1 seed patches (00039) remain intact.
DELETE FROM public.labels WHERE id IN (
  'night-watch',
  'reveille',
  'weekend-op',
  'graveyard-shift',
  'zero-dark-thirty',
  'marathon-runner',
  'sprinter-patch',
  'ironman',
  'iron-will',
  'sniper',
  'overwatch',
  'specialist',
  'scout',
  'joint-ops',
  'polyglot-commander',
  'cartographer',
  'legal',
  'med-evac',
  'intel',
  'ai-handler',
  'vim-ranger',
  'centurion',
  'ide-commando',
  'cqc'
);
-- +goose StatementEnd
