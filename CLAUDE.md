# Project Instructions for AI Agents

This file provides instructions and context for AI coding agents working on this project.

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


## Build & Test

_Add your build and test commands here_

```bash
# Example:
# npm install
# npm test
```

## Architecture Overview

_Add a brief overview of your project architecture_

## Conventions & Patterns

_Add your project-specific conventions here_

## Encryption at Rest (gaka-6jm.2)

Boomtime encrypts user-scoped secrets (currently: imported Wakatime API keys)
under AES-256-GCM. The symmetric key comes from the `BOOM_ENCRYPTION_KEY` env
var (base64-encoded 32 bytes).

**Generate one:** `openssl rand -base64 32`

**Set it in `.env` before booting** — behavior depends on `BOOM_ENV`:

- `dev` / `test` (unset defaults to `prod` per config): missing key logs a
  WARNING and any save/read path errors out. Local flows without the feature
  keep working.
- `prod` / `production` (gaka-6jm.9): missing / invalid key = boomtime exits
  at startup with a clear log. Prevents "silently didn't persist a single key
  for a month" incidents.

**Never log the plaintext** of any encrypted secret; never return it via the
API. The GET endpoint deliberately reports only `{"hasSavedKey": bool}` — no
hint.

### Key Rotation (gaka-6jm.7)

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
reports the affected username — no partial rotation is possible.

### Save-on-Success (gaka-6jm.8) + Key Status (gaka-6jm.10)

Import flow: a typed Wakatime key travels with the job but is only
persisted to `users.encrypted_wakatime_key` when the run completes without
seeing any wakatime.com 401. On a 401, `wakatime_key_status` flips to
`invalid` and the typed key is NOT saved. See `importer.applyKeyOutcome`.

### Backups Include Encrypted Secrets (gaka-awh.3)

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
