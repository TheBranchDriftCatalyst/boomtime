// Spaces — named, rule-based scopes (a Space per context: work/personal/…).

// How a Space rule's matchValue is interpreted. Membership is exact|regex only
// (template is a transform, meaningless for membership).
export type SpaceMatchType = "exact" | "regex";

export interface SpaceRule {
  id: number;
  axis: string;
  matchValue: string;
  matchType: SpaceMatchType;
}

// GET /spaces — the list rows.
export interface Space {
  id: number;
  name: string;
  position: number;
  ruleCount: number;
}

// GET /spaces/:id — a single Space with its membership rules.
export interface SpaceDetail {
  id: number;
  name: string;
  position: number;
  rules: SpaceRule[];
}

export interface AddSpaceRuleBody {
  axis: string;
  matchValue: string;
  matchType: SpaceMatchType;
}

// GET /spaces/preview — the raw values an (unsaved) rule currently matches.
interface SpacePreviewValue {
  value: string;
  count: number;
}

export interface SpacePreview {
  values: SpacePreviewValue[];
  truncated: boolean;
}
