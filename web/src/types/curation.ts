// Data curation (non-destructive hides + persistent rename rules).

type CurationAction = "hide" | "rename";

// How a rename rule's matchValue is interpreted. Hide rules are always exact.
// How a rename rule's matchValue is interpreted:
//  - exact:    literal value == matchValue
//  - regex:    matchValue is a regex; matching values map to newValue
//  - template: matchValue is a regex, newValue is a regexp_replace template with
//              `\1` backrefs (e.g. `^@(.*)$` + `\1` strips a leading `@`).
export type CurationMatchType = "exact" | "regex" | "template";

export interface CurationRule {
  id: number;
  axis: string;
  action: CurationAction;
  matchValue: string;
  newValue: string | null;
  // Defaults to "exact" when the backend omits it (older rules / hide rules).
  matchType?: CurationMatchType;
  createdAt: string;
}

export interface AddCurationRuleBody {
  axis: string;
  action: CurationAction;
  matchValue: string;
  newValue?: string;
  matchType?: CurationMatchType;
}

export interface AddCurationRulePayload {
  rule: CurationRule;
}

// GET /api/v1/users/current/curation/:id/affected — the raw values a rule
// currently matches, with their heartbeat counts and (for regex/template rules)
// the value they map to in the dashboards.
interface CurationAffectedValue {
  value: string;
  count: number;
  // The mapped-to value for this raw value (exact/regex: the rule's newValue;
  // template: regexp_replace applied). Optional until the backend emits it.
  mappedTo?: string;
}

export interface CurationAffectedPayload {
  values: CurationAffectedValue[];
  truncated?: boolean;
}

// gaka-cr4 + gaka-due: destructive curation action payloads (apply for rename
// rules, purge for hide rules). ONE preview endpoint dispatches on
// rule.action; per-action verbs run the actual mutation.
//
// PREVIEW response is a discriminated union on `action`:
//   - apply: heartbeats.<col> is UPDATED to a new value (rows survive)
//   - purge: matching heartbeats are DELETED (rows cease to exist)
// Both variants also delete the curation_rules row itself. The frontend's
// DestructiveActionDialog renders both from the same shape.

// One row that would be rewritten by a rename apply. `before` and `after`
// are the raw column values on that heartbeat.
export interface ApplyRenamePreviewRow {
  id: number;
  before: string;
  after: string;
}

// One row that would be DELETED by a hide purge. `deleted` holds the raw
// column values on that heartbeat (currently a single {col: value} pair —
// the raw column of the rule's axis — but shaped as an object so the modal
// can render multi-column diffs later without a schema change).
export interface PurgeHiddenPreviewRow {
  id: number;
  deleted: Record<string, string>;
}

// Shared preview envelope — action discriminates the row shape (and the
// verbs on the SQL strings: sqlUpdate for apply, sqlDeleteRows for purge).
export type CurationActionPreviewPayload =
  | {
      // Rename apply — an UPDATE of the raw column + a DELETE of the rule row.
      action: "rename";
      // Combined "UPDATE ...;\nDELETE ...;" — convenient for a single <pre> block.
      sqlPlanned: string;
      sqlUpdate: string;
      sqlDelete: string; // deletes the curation_rules row
      affectedRows: ApplyRenamePreviewRow[];
      totalAffected: number;
      rowsShown: number;
      rule: {
        id: number;
        axis: string;
        action: "rename";
        matchType?: CurationMatchType;
        matchValue: string;
        newValue: string | null;
      };
    }
  | {
      // Hide purge — a DELETE of matching heartbeats + a DELETE of the rule row.
      action: "hide";
      sqlPlanned: string;
      // The DELETE against `heartbeats` — the destructive one.
      sqlDeleteRows: string;
      // The DELETE against `curation_rules` — removes the rule row itself.
      sqlDeleteRule: string;
      affectedRows: PurgeHiddenPreviewRow[];
      totalAffected: number;
      rowsShown: number;
      rule: {
        id: number;
        axis: string;
        action: "hide";
        matchType?: CurationMatchType;
        matchValue: string;
        newValue: string | null;
      };
    };

// APPLY response (rename rules only): the number of heartbeat rows actually
// rewritten and the exact SQL that ran (must match sqlPlanned from the
// preview verbatim — the backend regression test TestApplyRenamePreviewMatchesRun
// guards this).
export interface ApplyRenamePayload {
  rowsAffected: number;
  sqlRun: string;
  sqlUpdate: string;
  sqlDelete: string;
}

// PURGE response (hide rules only): the number of heartbeat rows deleted and
// the exact SQL that ran. Backend regression TestPurgeHiddenPreviewMatchesRun
// guards preview===run identity, same as the apply path.
export interface PurgeHiddenPayload {
  rowsAffected: number;
  sqlRun: string;
  sqlDeleteRows: string;
  sqlDeleteRule: string;
}
