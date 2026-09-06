-- +goose Up
-- +goose StatementBegin

-- boom-inih: scrub plaintext wakatime.com API keys out of import_jobs.value.
--
-- Until this migration the import submit handler resolved the run's key (the
-- key the user typed, the DECRYPTED users.encrypted_wakatime_key, or the
-- server-wide BOOM/WAKATIME_API_KEY) into the QueueItem it marshalled into
-- import_jobs.value. Nothing ever cleared that column, and
-- internal/shared/db/dump.go ships import_jobs WITH `value` in
-- GET /api/v1/users/current/db/export — so every whole-DB backup ZIP carried
-- plaintext keys, defeating the boom-6jm encryption-at-rest threat model
-- ("an attacker with the backup still needs the env-side symmetric key").
-- On the no-typed-key path it was the OPERATOR's server-wide key sitting in a
-- user-scoped backup.
--
-- The code fix (importer.QueueItem.MarshalJSON + importer.resolveToken) stops
-- new rows from carrying a key; this migration retires the historical ones.
--
-- SECURITY: this statement deliberately has no RETURNING clause, no RAISE
-- NOTICE, and no SELECT of `value` — the data being removed IS the secret, and
-- surfacing it in the migration output would relocate the leak into the
-- server / CI logs rather than remove it.
--
-- Shape notes:
--   * `jsonb - 'key'` removes a key from an object and is a no-op when the key
--     is absent, so the rewrite is total for object-valued rows.
--   * `jsonb #- path` is avoided: it raises on a non-object at a path segment.
--     Rows written by tests can hold `{}` or even a JSON array, so every
--     access is guarded by jsonb_typeof first.
--   * The WHERE clause keeps this to the (small) set of rows that actually
--     hold a token, so a re-run is free and no untouched row is rewritten.
UPDATE public.import_jobs
SET value = CASE
        WHEN jsonb_typeof(value -> 'reqPayload') = 'object'
            THEN jsonb_set(value, '{reqPayload}',
                           (value -> 'reqPayload') - 'apiToken', false) - 'typedToken'
        ELSE value - 'typedToken'
    END
WHERE jsonb_typeof(value) = 'object'
  AND (
        value ? 'typedToken'
        OR (jsonb_typeof(value -> 'reqPayload') = 'object'
            AND (value -> 'reqPayload') ? 'apiToken')
      );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Irreversible by construction: the removed values were plaintext secrets and
-- are not recoverable (nor should they be). Rolling back leaves the scrubbed
-- rows scrubbed — the importer reads no token from import_jobs.value on any
-- code path (db.Job does not even select the column), so nothing depends on
-- them being present.
SELECT 1;

-- +goose StatementEnd
