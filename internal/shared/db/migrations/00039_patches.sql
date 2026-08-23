-- +goose Up
-- +goose StatementBegin

-- Memewarfare protocols wave 1 (boom-mwp): add `patch` as a 5th label kind.
--
-- ADDITIVE — the existing 114 labels (tier/archetype/tribe/meme) stay put.
-- Patches sit alongside them as a new dimension:
--   tier      → hierarchical progression per axis (LANG MASTER, etc.)
--   archetype → sustained-behavior pattern (late-night-coder, machine)
--   tribe     → identity by toolstack (vim-enjoyer, mac-native)
--   meme      → memecore character labels (SIGMA GRINDSET)
--   patch     ← THIS: event-driven military-op awards, "sweet patches"
--                 with distinct visual chrome (double-amber border + ★).
--
-- Wave 1 (this migration) wires the kind + ships 6 proof-of-concept
-- patches (RRT / FIRE FIGHTER / FIRST RESPONDER / RECON / SIGNAL CORPS /
-- ORDNANCE). Wave 2 (background impl agent) expands the patch catalog
-- to ~30 more without touching existing labels.

ALTER TABLE public.labels DROP CONSTRAINT IF EXISTS labels_kind_check;
ALTER TABLE public.labels
  ADD CONSTRAINT labels_kind_check
  CHECK (kind IN ('tier', 'archetype', 'tribe', 'meme', 'patch'));

-- Wave-1 patch seed. Ranks stay in the 200+ band so patches always
-- outrank other kinds on the hero tagline slice (memecore is 100-199).
-- Every patch has:
--   glyph          — visual prefix rendered in the chip
--   description    — narrative shown in the tooltip
--   optimized_prompt — SDXL tag-list for the ComfyUI-generated image
--   condition      — evaluator JSONB (see evaluator.ts for kinds)
--
-- Conditions here reuse existing evaluator kinds only (punchcard-hour-pct,
-- distinct-count, daily-avg, trend, all). No new evaluator machinery needed.

INSERT INTO public.labels
  (id, kind, label, glyph, description, optimized_prompt, rank, tier, condition)
VALUES

-- ── RAPID RESPONSE TEAM ─────────────────────────────────────────────
('rapid-response-team', 'patch', 'RAPID RESPONSE TEAM',  '⚡',
 'Deployed when the mission calls at 0-dark-hundred. ≥15% of your activity lands between 22:00 and 03:00 with a sustained daily average — you answer when the pager screams.',
 'cyberpunk-anime chibi military operator in tactical gear, red glow-visor, night ops loadout, radio headset with amber LED, deep crimson rim light on jet black, RRT patch on shoulder, urgent posture, no text',
 220, '',
 '{"kind":"all","of":[
     {"kind":"punchcard-hour-pct","hoursIn":[22,23,0,1,2],"op":">=","pct":0.15},
     {"kind":"daily-avg","op":">=","hours":3}
   ]}'),

-- ── FIRE FIGHTER ─────────────────────────────────────────────────────
('fire-fighter', 'patch', 'FIRE FIGHTER', '🔥',
 'The alarms went off and you ran TOWARD the blaze. Recent 7-day average ≥ 2× the prior 7-day baseline — the incident-response spike is unmistakable.',
 'cyberpunk-anime chibi coder in fire-marshal turnout gear, red helmet with amber visor, running toward glowing terminal on fire, half-charred code streaming behind, deep crimson rim light, jet black background, no text',
 215, '',
 '{"kind":"trend","op":">=","ratio":2.0}'),

-- ── FIRST RESPONDER ──────────────────────────────────────────────────
('first-responder', 'patch', 'FIRST RESPONDER', '☀',
 'On-scene before the sun. ≥20% of your keystrokes land between 05:00 and 09:00 — the ambulance rolls out of the garage before anyone else has coffee.',
 'cyberpunk-anime chibi coder in EMT-tactical vest, glowing amber sunrise silhouette behind, dew on the keyboard, red radio squelch icon overhead, first-responder cross patch on shoulder, deep crimson rim light on jet black, no text',
 214, '',
 '{"kind":"punchcard-hour-pct","hoursIn":[5,6,7,8],"op":">=","pct":0.20}'),

-- ── RECON ────────────────────────────────────────────────────────────
('recon', 'patch', 'RECON', '🎯',
 'Scouting the terrain — touched 5+ distinct projects with meaningful time. You map before you strike.',
 'cyberpunk-anime chibi operator in ghillie-cloak techwear, holo-map floating projected from wrist, multiple project waypoints tagged in crimson, kneeling posture behind a ridge, red rim light on jet black, RECON patch on shoulder, no text',
 213, '',
 '{"kind":"distinct-count","axis":"projects","min_hours":5,"op":">=","count":5}'),

-- ── SIGNAL CORPS ─────────────────────────────────────────────────────
-- Meeting + docs + writing tests — the coordination classes. If your
-- categories skew heavily to communication/coordination work.
('signal-corps', 'patch', 'SIGNAL CORPS', '📡',
 'The wires must stay hot. ≥15% of activity in meetings/docs/comms categories — you keep the operation from going dark.',
 'cyberpunk-anime chibi radio operator in tactical vest, headset with glowing crimson LEDs, satellite dish antenna extending from backpack, signal-strength bars overhead, cables coiled at feet, deep crimson rim light on jet black, SIGNAL CORPS patch, no text',
 212, '',
 '{"kind":"axis-pct","axis":"categories","value":"meeting","op":">=","pct":0.15}'),

-- ── ORDNANCE ─────────────────────────────────────────────────────────
-- test-obsessive equivalent — you build the ammo, everyone else fires it.
('ordnance', 'patch', 'ORDNANCE', '💣',
 'Every shell counts. ≥5% of activity in the writing-tests category — you build the ammunition the rest of the squad depends on.',
 'cyberpunk-anime chibi coder in bomb-disposal apron, tinkering with a glowing test-canister, crates of ammo labeled "unit" and "e2e" stacked behind, red targeting-reticle overhead, deep crimson rim light on jet black, ORDNANCE patch on shoulder, no text',
 211, '',
 '{"kind":"axis-pct","axis":"categories","value":"writing tests","op":">=","pct":0.05}');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM public.labels WHERE kind = 'patch';
ALTER TABLE public.labels DROP CONSTRAINT IF EXISTS labels_kind_check;
ALTER TABLE public.labels
  ADD CONSTRAINT labels_kind_check
  CHECK (kind IN ('tier', 'archetype', 'tribe', 'meme'));
-- +goose StatementEnd
