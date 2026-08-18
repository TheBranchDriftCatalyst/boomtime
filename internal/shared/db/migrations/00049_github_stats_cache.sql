-- +goose Up
-- +goose StatementBegin

-- Per-user GitHub stats cache (gaka-anh Phase 2). ONE ROW PER USER — the
-- `username` PRIMARY KEY is the idempotency backbone: every sync does an
-- INSERT ... ON CONFLICT (username) DO UPDATE (see
-- internal/db/github_stats_cache.go UpsertGithubStatsCache), so re-running a
-- sync REPLACES the row wholesale and can never accrue duplicate stats. The
-- GraphQL contributionsCollection returns the FULL trailing-year daily grid in
-- one call, so `contribution_grid_json` is stored wholesale (replace) — we
-- NEVER append per-day rows.
--
--   login                  — the GitHub @handle captured at sync time so the
--                            public (unauthed) stats read can render a name
--                            WITHOUT touching the encrypted token / users row.
--   totals_json            — a SINGLE extensible JSONB blob of all aggregate
--                            metrics (model.GithubTotals): commits, pull
--                            requests, PR reviews, issues, repositories,
--                            restricted/private count, total contributions,
--                            followers, following, stars, public repos, public
--                            gists, account age. Kept as one blob ON PURPOSE so
--                            adding another aggregate later needs NO migration —
--                            just a new field on the Go struct.
--   contribution_grid_json — [{date:"YYYY-MM-DD", count:int}] for the trailing
--                            year (model.GithubContributionDay). Replaced whole.
--   top_repos_json         — [{name,stars,language,url}] top-N by stars
--                            (model.GithubTopRepo).
--   languages_json         — [{name,bytes}] aggregate byte-counts across the
--                            user's repos (model.GithubLanguage).
--   fetched_at             — wall-clock of this sync; drives the handler-layer
--                            TTL (serve cache when < ~1h old, else re-sync).
--
-- ON DELETE CASCADE off users(username): dropping a user reaps their cache row.
-- No encrypted material lives here — only public GitHub aggregates — so this
-- table is safe to include in the whole-DB backup without the BOOM_ENCRYPTION_KEY
-- threat-model concerns that gate encrypted_github_token.
CREATE TABLE public.github_stats_cache (
    username               text PRIMARY KEY REFERENCES public.users(username) ON DELETE CASCADE,
    login                  text        NOT NULL DEFAULT '',
    totals_json            jsonb       NOT NULL DEFAULT '{}'::jsonb,
    contribution_grid_json jsonb       NOT NULL,
    top_repos_json         jsonb       NOT NULL,
    languages_json         jsonb       NOT NULL,
    fetched_at             timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.github_stats_cache;
-- +goose StatementEnd
