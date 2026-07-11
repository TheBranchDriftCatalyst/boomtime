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
