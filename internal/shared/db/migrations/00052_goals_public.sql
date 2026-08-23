-- +goose Up
-- +goose StatementBegin

-- boom-wpb (Part B Stage 4): goals.public — per-goal opt-in to appear on the
-- owner's embeddable goal widgets (goal-progress / goal-ring / goal-list).
-- Goals stay PRIVATE BY DEFAULT: a goal only reaches the public
-- /widget/svg/... endpoint when BOTH enabled AND public are true. Default
-- false so every existing goal stays invisible to the world until its owner
-- explicitly flips it on — no silent exposure on deploy. No account-level
-- master switch; each goal opts in individually.
ALTER TABLE public.goals
    ADD COLUMN public boolean NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.goals DROP COLUMN IF EXISTS public;
-- +goose StatementEnd
