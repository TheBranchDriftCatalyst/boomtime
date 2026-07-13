-- +goose Up

-- gaka-1l9: capture wakatime.com's AI-assistance heartbeat fields that
-- started arriving on 2026-07-03 (schema drift observed in v0.5.2's
-- DriftBanner). Persisting them here unlocks an "AI Assistance" surface —
-- per-day AI vs human line-change split, prompt token totals, session
-- counts — without needing to re-run imports later.
--
-- All columns are nullable; a heartbeat from a plugin that doesn't emit
-- these fields (older editor plugins, non-AI clients) simply stores NULL,
-- and every downstream aggregation COALESCEs to 0 / ignores nulls. No
-- rollup churn because entity/AI-per-row metrics aren't rollup axes and
-- the rollup query doesn't touch these columns.
--
-- Storage cost: 4 x INT nullable + 3 x TEXT nullable on a ~500k-row table
-- is a few MB — trivial compared to the trigram indexes.
ALTER TABLE heartbeats
    ADD COLUMN ai_input_tokens     INTEGER,
    ADD COLUMN ai_output_tokens    INTEGER,
    ADD COLUMN ai_line_changes     INTEGER,
    ADD COLUMN human_line_changes  INTEGER,
    ADD COLUMN ai_prompt_length    INTEGER,
    ADD COLUMN ai_session          TEXT,
    ADD COLUMN ai_subscription_plan TEXT;

-- +goose Down
ALTER TABLE heartbeats
    DROP COLUMN ai_input_tokens,
    DROP COLUMN ai_output_tokens,
    DROP COLUMN ai_line_changes,
    DROP COLUMN human_line_changes,
    DROP COLUMN ai_prompt_length,
    DROP COLUMN ai_session,
    DROP COLUMN ai_subscription_plan;
