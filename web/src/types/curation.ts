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

// gaka-cr4: destructive-apply preview + apply payloads. See
// internal/handler/curation.go (ApplyRenamePreview / ApplyRename) and the
// modal at web/src/features/curation/ApplyMappingDialog.tsx.
//
// PREVIEW response: the exact UPDATE + DELETE SQL that a destructive apply
// would run, plus a capped diff of every heartbeat row that would be
// rewritten. `totalAffected` is exact; `affectedRows` is capped at 100 (the
// modal renders an "and N more…" footer).
export interface ApplyRenamePreviewRow {
  id: number;
  before: string;
  after: string;
}
export interface ApplyRenamePreviewPayload {
  // Combined "UPDATE ...;\nDELETE ...;" — convenient for a single <pre> block.
  sqlPlanned: string;
  sqlUpdate: string;
  sqlDelete: string;
  affectedRows: ApplyRenamePreviewRow[];
  totalAffected: number;
  rowsShown: number;
  rule: {
    id: number;
    axis: string;
    matchType?: CurationMatchType;
    matchValue: string;
    newValue: string | null;
  };
}

// APPLY response: the number of heartbeat rows actually rewritten and the
// exact SQL that ran (must match sqlPlanned from the preview verbatim — the
// backend regression test TestApplyRenamePreviewMatchesRun guards this).
export interface ApplyRenamePayload {
  rowsAffected: number;
  sqlRun: string;
  sqlUpdate: string;
  sqlDelete: string;
}
