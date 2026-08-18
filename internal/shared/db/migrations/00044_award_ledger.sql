-- +goose Up
-- +goose StatementBegin

-- Award ledger (gaka-mwp-streaks): persist WHICH labels fired for a user
-- in a given period so we can display streak badges ("3x NIGHT WATCH")
-- on the LabelChip.
--
-- Current model: evaluator is JIT client-side, no history. New model:
-- client POSTs the firing labels after each evaluate() run; server upserts
-- one row per (user, label, period_start). Streak = walk backward from
-- current period, count consecutive periods until first gap.
--
-- Period is computed FROM THE USER'S TIMEZONE (gaka-dg7 resolver already
-- exists) so a "daily" streak in Pacific isn't broken by UTC day-flip.
--
-- ----------------------------------------------------------------------
-- Kind-based period defaults (applied when labels.period_default is '')
-- Also documents intent for the FE + admin UI:
--   tier      → lifetime  (once earned, no recurrence; ledger not written)
--   tribe     → lifetime  (identity by toolstack; not a recurring reward)
--   archetype → weekly    (sustained-behavior pattern; check-in cadence)
--   meme      → weekly    (memecore character labels; same cadence as archetype)
--   patch     → daily     (event-driven military-op awards; daily rhythm)
-- Per-label override lives in labels.period_default. Empty = use default.

ALTER TABLE public.labels
  ADD COLUMN period_default TEXT NOT NULL DEFAULT ''
  CONSTRAINT labels_period_default_check
    CHECK (period_default IN ('', 'daily', 'weekly', 'monthly', 'lifetime'));

-- award_ledger: idempotent per (username, label_id, period_start) — the
-- POST /awards/log endpoint upserts, so repeated visits within the same
-- period don't create duplicate rows or move the streak needle.
CREATE TABLE public.award_ledger (
  username     TEXT        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
  label_id     TEXT        NOT NULL REFERENCES public.labels(id) ON DELETE CASCADE,
  period_type  TEXT        NOT NULL CHECK (period_type IN ('daily', 'weekly', 'monthly')),
  period_start TIMESTAMPTZ NOT NULL,
  period_end   TIMESTAMPTZ NOT NULL,
  logged_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (username, label_id, period_start)
);

-- Streak query walks backward from now by period, so a
-- (username, label_id, period_start DESC) index is the hot path.
CREATE INDEX award_ledger_user_label_period_idx
  ON public.award_ledger (username, label_id, period_start DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE public.award_ledger;
ALTER TABLE public.labels DROP COLUMN period_default;
-- +goose StatementEnd
