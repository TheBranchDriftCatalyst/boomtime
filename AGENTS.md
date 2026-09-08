# Agent Instructions

Instructions and context for AI coding agents working on boomtime.

`CLAUDE.md` is a symlink to this file — edit `AGENTS.md` and both stay in sync.
The generated Beads sections at the bottom are managed by
`bd setup claude` / `bd setup codex` — edit above them, never inside them.

## Build & Test

```bash
# Go — the whole tree, backend and CLI
go build ./...
go vet ./...                    # compiles test files too; build alone does not
go test ./...

# Web (from web/)
yarn dev            # vite dev server
yarn build          # tsc -b --noEmit --force && vite build
yarn typecheck      # tsc -b --noEmit --force
yarn lint           # eslint
yarn test           # vitest run
yarn e2e            # playwright

# Task runner (see Taskfile.yml for the full list)
task dev            # backend + frontend together
task db:up          # local Postgres
task migrate        # apply goose migrations
task db:reset       # drop and re-migrate
```

**Tests need a Postgres.** They read `BOOM_DB_HOST` / `BOOM_DB_PORT` /
`BOOM_DB_NAME` / `BOOM_DB_USER` / `BOOM_DB_PASS` (note `_PASS`, not
`_PASSWORD`). Two traps that cost real debugging time:

- If a local k8s/Tilt pod is forwarding `localhost:5432`, DB-backed specs
  silently **skip** rather than fail. Point `BOOM_DB_HOST` at the machine's LAN
  address to reach the real container. `BOOM_REQUIRE_DB=1` turns a skip into a
  hard failure, which is what you want in CI and when verifying a fix.
- Suites create long-lived isolated `boomtime_test*` databases and record the
  goose version. **After adding a migration, drop every `boomtime_test*`
  database**, or specs run against a stale schema and fail in ways that look
  like code defects.

`yarn typecheck` passing locally does **not** guarantee CI passes: CI's Docker
web stage resolves types in isolation. Verify with `docker build --target web`.

## Architecture Overview

Go backend (Echo v5) serving an embedded React/TypeScript SPA, on Postgres.
One binary, `go:embed`-ed frontend.

```
cmd/boomtime          host binary (all domains)
cmd/catalyst-books    standalone books binary (own image + DB, single-owner)
internal/shared       cross-domain: db, config, server, auth, apiroute, openapi
internal/boomtime     coding analytics: heartbeats, stats, goals, widgets, curation
internal/books        reading: kindle/audible ingest, hardcover sync, liberation
internal/identity     auth, OIDC, avatars, websockets
internal/jobs         generic job queue (Postgres FOR UPDATE SKIP LOCKED)
internal/domainreg    builds the module registry the composition root consumes
```

**Domains are `catalyst.Module`s.** A module stashes the handler it builds so
the composition root can late-wire dependencies onto it afterwards:

```go
func (m *Module) RegisterRoutes(e *echo.Echo, d catalyst.Deps) {
    m.h = booksapi.New(d.DB, d.Cfg, d.Logger)   // REASSIGNED on every call
    booksapi.Register(e, m.h)
}
```

Therefore **never call `RegisterRoutes` twice against the same
`*catalyst.Registry`** — the second pass replaces the stashed handler, so
anything late-wired lands on the new one while the live routes still hold
method values bound to the old one. This shipped once and broke every
background-job enqueue path for a day while the Jobs page looked healthy.
`server.DocumentationRouter()` takes no registry precisely so the live one
cannot be passed to it.

**Migrations live in two trees** and both need the change:
`internal/shared/db/migrations/` (host) and `internal/books/db/migrations/`
(standalone books). Goose, numerically ordered — **the two trees are numbered
independently**, and the host sequence has gaps (there is no `00084`), so check
`ls` rather than assuming the next number.

The standalone tree **does have a `users` table** (`00001_books_baseline.sql`), and
per-user books tables keep the same `owner text NOT NULL REFERENCES
public.users(username) ON DELETE CASCADE` FK there as on the host. A new table is
therefore byte-identical across the two trees. (`book_liberation_attempts`
(`00004`) uses a plain `owner` column and its comment claims there is no users
table — it is the outlier, and the claim is wrong. Do not copy it.)

Adding a table also means: `dumpTables` in `internal/shared/db/dump.go`
(FK-parents first — `TestDumpSchemaCensus` enforces it), the hardcoded list in
`internal/books/db/migrate_standalone_test.go`, and a seed row in the dump
round-trip spec (it refuses a dumped table it cannot round-trip). Then **drop
every `boomtime_test*` database**, including `boomtime_books_schema_test`.

## Conventions & Patterns

- **Change detection has four named strategies, and they fail differently.**
  Before adding a source or an event type, read
  `docs/design/change-detection-patterns.md`. In short: `scalar-delta` (sample a
  number, diff consecutive samples) fails by *aliasing*; `set-reconcile` (diff the
  fetched key set against stored) fails by *absence-as-deletion*; `push` fails by
  delivery; `replicated` fails by echo loops. No single defence covers two of
  them. Set-reconcile sources go through `internal/books/reconcile`, whose one
  hard rule is that **a fetch returning nothing retires nothing**.
- **Event types are declared, and the declaration is enforced.**
  `internal/books/events` registers every event the extraction layer produces,
  mirroring `internal/shared/query/domains.go` on the read side. Declare `Time`
  honestly: `events.Validate` REJECTS an event carrying a timestamp on a type
  declared `TimeUnknown`. **Never fabricate an event time** — a Kindle highlight
  has none, so `captured_at` stays NULL rather than being stamped with sync time,
  which would date the whole corpus to the first sync.
- **The OpenAPI spec is derived, never hand-written.** Register routes through
  the typed seam in `internal/shared/apiroute` so request/response schemas come
  from the Go types. `openapi_quality_test.go` is a **ratchet**: its ceilings
  (placeholder schemas, weak prose, undocumented responses) may only go down.
  A failure there means document the route, not raise the ceiling.
- **Feature flags default off**, and flag-off must be byte-identical to the
  behaviour before the flag existed.
- **Regression tests must be mutation-verified.** Write the test first, watch it
  fail reproducing the actual symptom, then fix. A test that never went red
  proves only that it agrees with the code it was written against.
- **Never log or return the plaintext of an encrypted secret.** The public
  profile/key endpoints deliberately report only `{"hasSavedKey": bool}`.
- This repository is **public**. Audit reports, findings with exploit detail,
  and anything naming a live credential stay out of the tree.
- Before pushing, run `git log --oneline origin/main..HEAD` and confirm only
  intended commits are going up. Pushes deploy via ArgoCD, and a shared
  checkout means `git push` carries every ancestor of HEAD — including other
  agents' commits.

## Non-Interactive Shell Commands

**ALWAYS use non-interactive flags** with file operations to avoid hanging on confirmation prompts.

Shell commands like `cp`, `mv`, and `rm` may be aliased to include `-i` (interactive) mode on some systems, causing the agent to hang indefinitely waiting for y/n input.

**Use these forms instead:**
```bash
# Force overwrite without prompting
cp -f source dest           # NOT: cp source dest
mv -f source dest           # NOT: mv source dest
rm -f file                  # NOT: rm file

# For recursive operations
rm -rf directory            # NOT: rm -r directory
cp -rf source dest          # NOT: cp -r source dest
```

An alias survives `-f`; `command cp -f` bypasses it entirely when even that
prompts. `zsh` also sets `noclobber` in this environment, so `>` on an existing
file errors — use `>|` to truncate deliberately.

**Other commands that may prompt:**
- `scp` - use `-o BatchMode=yes` for non-interactive
- `ssh` - use `-o BatchMode=yes` to fail instead of prompting
- `apt-get` - use `-y` flag
- `brew` - use `HOMEBREW_NO_AUTO_UPDATE=1` env var

## Encryption at Rest (boom-6jm.2)

Boomtime encrypts user-scoped secrets (currently: imported Wakatime API keys)
under AES-256-GCM. The symmetric key comes from the `BOOM_ENCRYPTION_KEY` env
var (base64-encoded 32 bytes).

**Generate one:** `openssl rand -base64 32`

**Set it in `.env` before booting** — behavior depends on `BOOM_ENV`:

- `dev` / `test` (unset defaults to `prod` per config): missing key logs a
  WARNING and any save/read path errors out. Local flows without the feature
  keep working.
- `prod` / `production` (boom-6jm.9): missing / invalid key = boomtime exits
  at startup with a clear log. Prevents "silently didn't persist a single key
  for a month" incidents.

**Never log the plaintext** of any encrypted secret; never return it via the
API. The GET endpoint deliberately reports only `{"hasSavedKey": bool}` — no
hint.

### Key Rotation (boom-6jm.7)

Rotating `BOOM_ENCRYPTION_KEY` while ciphertext exists in the DB would strand
every saved key (Decrypt fails auth). Use the built-in re-encrypt command:

```bash
# 1. generate the new key
NEW=$(openssl rand -base64 32)
OLD=$BOOM_ENCRYPTION_KEY

# 2. offline (server ideally stopped): re-encrypt every users row in one tx
boomtime rotate-encryption-key --old "$OLD" --new "$NEW"

# 3. update BOOM_ENCRYPTION_KEY=$NEW in your env, restart boomtime
```

The command decrypts every `users.encrypted_wakatime_key` under `--old`,
re-encrypts under `--new`, and commits in a single transaction. If ANY row
fails to decrypt under `--old`, the command aborts BEFORE any write and
reports the affected username — no partial rotation is possible. Rotate BEFORE
flipping the env value; a hot swap strands existing ciphertext.

### Save-on-Success (boom-6jm.8) + Key Status (boom-6jm.10)

Import flow: a typed Wakatime key travels with the job but is only
persisted to `users.encrypted_wakatime_key` when the run completes without
seeing any wakatime.com 401. On a 401, `wakatime_key_status` flips to
`invalid` and the typed key is NOT saved. See `importer.applyKeyOutcome`.

### Backups Include Encrypted Secrets (boom-awh.3)

The whole-DB backup (`GET /api/v1/users/current/db/export`) includes the
`users.encrypted_wakatime_key` ciphertext column (plus `wakatime_key_status`,
`wakatime_key_checked_at`, `public_profile_enabled`, and `public_slug`). The
`.env` file is NEVER included in the ZIP — only Postgres tables are exported —
so `BOOM_ENCRYPTION_KEY` stays out of every backup. This preserves the same
threat model as password hashes: an attacker with the backup still needs the
env-side symmetric key to recover plaintext Wakatime keys.

**Restoring across environments requires the same `BOOM_ENCRYPTION_KEY`.**
If you migrate a backup to a new host, set `BOOM_ENCRYPTION_KEY` to the value
that was in effect when the backup was taken BEFORE calling the import
endpoint. `RestoreAll` refuses to load a backup that contains ciphertext when
the current process has no `BOOM_ENCRYPTION_KEY` set (400 error, no TRUNCATE
runs) — better to fail loudly than silently orphan every user's saved key
under an unknown symmetric key.

## Beads Notes

Workspace-wide findings and recovery procedures: `../BEADS.md`.

**NEVER run `pkill -f "dolt sql-server"`.** That pattern kills every repo's beads
server across the whole workspace, not just this one — it is the confirmed cause of
cross-repo tracker breakage. Use `bd dolt stop` (or the repo-scoped
`bd dolt killall`) from inside the target repo instead.

**If the tracker suddenly looks empty, do NOT recreate issues.** The usual cause is
`dolt_mode` flipping to `server` without a matching data dir, which silently
repoints bd at an empty directory while the real database sits elsewhere:

```bash
git diff .beads/metadata.json           # is dolt_mode flipped and uncommitted?
du -sh .beads/dolt .beads/embeddeddolt  # which one actually holds data?
```

A Dolt directory looks near-empty to `ls` because content lives under `.dolt/noms`;
never conclude "the database is empty" from a directory listing. This repo is
healthy: server mode, data in `.beads/dolt`, no `dolt_data_dir` key.

`bd` runs clean here with **no environment overrides** — do not prefix calls
with `BD_IGNORE_SCHEMA_SKEW=1`. If `bd doctor` reports a schema-version skew,
run `bd migrate status` (it updates the Dolt metadata in place); if it reports
outdated hooks, run `bd hooks install --force`. The override suppresses the
signal without resolving it.

`.beads/issues.jsonl` is a passive export and does **not** refresh itself —
regenerate with `bd export -o .beads/issues.jsonl` after bulk changes or an
issue-prefix rename, or a fresh clone reads stale history.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:970c3bf2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   bd dolt push
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->

<!-- BEGIN BEADS CODEX SETUP: generated by bd setup codex -->
## Beads Issue Tracker

Use Beads (`bd`) for durable task tracking in repositories that include it. Use the `beads` skill at `.agents/skills/beads/SKILL.md` (project install) or `~/.agents/skills/beads/SKILL.md` (global install) for Beads workflow guidance, then use the `bd` CLI for issue operations.

### Quick Reference

```bash
bd ready                # Find available work
bd show <id>            # View issue details
bd update <id> --claim  # Claim work
bd close <id>           # Complete work
bd prime                # Refresh Beads context
```

### Rules

- Use `bd` for all task tracking; do not create markdown TODO lists.
- Run `bd prime` when Beads context is missing or stale. Codex 0.129.0+ can load Beads context automatically through native hooks; use `/hooks` to inspect or toggle them.
- Keep persistent project memory in Beads via `bd remember`; do not create ad hoc memory files.

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.
<!-- END BEADS CODEX SETUP -->
