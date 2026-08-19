-- +goose Up
-- +goose StatementBegin

-- Fix (gaka-mwp): wave-1 seed patches (00039) shipped with two
-- condition JSON shapes that don't match the evaluator's expected
-- field names. Result: both patches silently never fire on any real
-- payload — the evaluator drops them without error.
--
-- Bugs found on wave-2 handoff:
--   recon         — used {min_hours, count} — evaluator expects {minHoursEach, n}
--   fire-fighter  — missing `window: "last7-vs-prior7"` on TrendCond
--                    (also renamed the ratio field from "ratio" — same)
--
-- 00039 is already applied to any environment past that revision, so
-- editing that file in place would only fix fresh checkouts. This
-- migration UPDATEs the rows in place. All 24 wave-2 patches (00040)
-- shipped with correct field names, so they don't need touching.
--
-- See web/shared/features/publicprofile/labels/types.ts for the source of
-- truth on evaluator kinds + field names.

UPDATE public.labels
   SET condition = '{"kind":"distinct-count","axis":"projects","minHoursEach":5,"op":">=","n":5}'::jsonb
 WHERE id = 'recon' AND kind = 'patch';

UPDATE public.labels
   SET condition = '{"kind":"trend","window":"last7-vs-prior7","op":">=","ratio":2.0}'::jsonb
 WHERE id = 'fire-fighter' AND kind = 'patch';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Revert to the (buggy) wave-1 shape. Down is provided for completeness;
-- reverting means those two patches go back to silently never firing.
UPDATE public.labels
   SET condition = '{"kind":"distinct-count","axis":"projects","min_hours":5,"op":">=","count":5}'::jsonb
 WHERE id = 'recon' AND kind = 'patch';

UPDATE public.labels
   SET condition = '{"kind":"trend","op":">=","ratio":2.0}'::jsonb
 WHERE id = 'fire-fighter' AND kind = 'patch';
-- +goose StatementEnd
