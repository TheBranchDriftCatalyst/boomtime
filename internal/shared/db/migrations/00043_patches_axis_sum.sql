-- +goose Up
-- +goose StatementBegin

-- Memewarfare wave 3 (boom-mwp): reinstate TERMINAL PURIST + FIELD MEDIC
-- now that the axis-time-sum evaluator kind exists.
--
-- Wave 2 agent dropped these two during expansion because the evaluator
-- had no way to combine hours across multiple axis values into a single
-- condition. TERMINAL PURIST wanted vim+neovim+emacs combined; FIELD
-- MEDIC wanted debugging+writing-tests combined. Both would have
-- required hacky OR-of-axis-time compositions that fired for casual
-- dabblers instead of true specialists.
--
-- axis-time-sum solves this: {kind:"axis-time-sum", axis, values:[...],
-- op, hours} → sums totalSeconds across every entry whose name matches
-- one of `values` (case-insensitive), then compares to the threshold.
-- See web/shared/features/publicprofile/labels/types.ts + conditions.ts.
--
-- Ranks stay in the 200+ patch band; slotted between existing patches
-- for a natural sort order.

INSERT INTO public.labels
  (id, kind, label, glyph, description, optimized_prompt, rank, tier, condition)
VALUES

-- ── TERMINAL VETERAN ───────────────────────────────────────────────────
-- ≥100h combined across vim + neovim + emacs — the "years of service"
-- volume signal. Complementary to the existing `terminal-purist` TRIBE
-- (00036 seed) which fires on per-editor >=90% share (identity, not
-- volume). Kept as a separate id so both can co-exist: tribe = "you ARE
-- a terminal purist", patch = "you have accumulated the hours to prove it".
('terminal-veteran', 'patch', 'TERMINAL VETERAN', '⌂',
 'The kernel does not require a mouse. ≥100 combined hours across vim/neovim/emacs — you have paid the tuition in keystrokes and the terminal recognizes its own.',
 'cyberpunk-anime chibi coder in tactical hooded cloak, tmux status bar glowing overhead like a HUD, keyboard with worn keycaps, red terminal cursors blinking in the pupils, deep crimson rim light on jet black, TERMINAL VETERAN patch on shoulder, no text',
 236, '',
 '{"kind":"axis-time-sum","axis":"editors","values":["vim","neovim","emacs"],"op":">=","hours":100}'),

-- ── FIELD MEDIC ────────────────────────────────────────────────────────
-- ≥30h combined across debugging + writing tests — the "fix + stabilize" operator.
-- Distinct from LEGAL (debugging >= 15% by percentage) — FIELD MEDIC is
-- ABSOLUTE hours across the recovery-work combo.
('field-medic', 'patch', 'FIELD MEDIC', '✚',
 'On the scene where systems bleed. ≥30 combined hours across debugging + writing tests — you patch the wound AND immunize the future.',
 'cyberpunk-anime chibi combat medic in tactical vest with red-cross patch, glowing debugger overlay held in one hand, syringe of test-serum in the other, wounded server-rack humming behind, deep crimson rim light on jet black, FIELD MEDIC patch on shoulder, no text',
 209, '',
 '{"kind":"axis-time-sum","axis":"categories","values":["debugging","writing tests"],"op":">=","hours":30}');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM public.labels WHERE id IN ('terminal-veteran', 'field-medic');
-- +goose StatementEnd
