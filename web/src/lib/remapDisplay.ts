// Client-side computation of what a raw value remaps to in the dashboards,
// given the user's rename curation rules. Backs BOTH the Heartbeats Explorer
// "→ {mapped}" badge and the RemappingForm live preview, so the two never drift.
//
// Rule precedence for a given axis+value (first match wins):
//   1. exact    — literal value === matchValue
//   2. regex    — new RegExp(matchValue).test(value) → newValue
//   3. template — regex match → value.replace(regex, jsTemplate)
//
// Template backref conventions:
//   - Backend/Postgres store templates with `\1` backrefs.
//   - JS String.prototype.replace uses `$1`. `templateToJs` converts `\N`→`$N`.
//   - The authoring UI accepts `$1` (familiar) and `templateToBackend` converts
//     `$N`→`\N` before sending, matching the backend's normalized form.

import type { CurationRule } from "@/types/api";

/** Convert Postgres-style `\N` backrefs to JS `$N` (for String.replace). */
export function templateToJs(template: string): string {
  // `\1` → `$1`. Leave other escapes untouched.
  return template.replace(/\\(\d)/g, "$$$1");
}

/** Convert authoring `$N` backrefs to backend `\N` (leaving `$$` as literal). */
export function templateToBackend(template: string): string {
  let out = "";
  for (let i = 0; i < template.length; i++) {
    const c = template[i];
    if (c === "$" && i + 1 < template.length) {
      const n = template[i + 1];
      if (n === "$") {
        out += "$"; // `$$` → literal `$`
        i++;
        continue;
      }
      if (n >= "0" && n <= "9") {
        out += "\\" + n; // `$N` → `\N`
        i++;
        continue;
      }
    }
    out += c;
  }
  return out;
}

/** Safely compile a regex; returns null on invalid patterns. */
function safeRegExp(pattern: string): RegExp | null {
  try {
    return new RegExp(pattern);
  } catch {
    return null;
  }
}

/**
 * Compute what `value` maps to under the given rename rules for its axis.
 * Returns null when no rule applies (so callers can skip the badge).
 *
 * `rules` may be the full rule list (any axis/action); it is filtered here.
 */
export function remapDisplay(
  axis: string,
  value: string | null,
  rules: CurationRule[] | undefined,
): string | null {
  if (value == null || !rules) return null;

  const axisRules = rules.filter(
    (r) => r.action === "rename" && r.axis === axis && r.newValue != null,
  );
  if (axisRules.length === 0) return null;

  // 1. exact.
  const exact = axisRules.find(
    (r) => (r.matchType ?? "exact") === "exact" && r.matchValue === value,
  );
  if (exact) return exact.newValue as string;

  // 2. regex.
  for (const r of axisRules) {
    if (r.matchType !== "regex") continue;
    const re = safeRegExp(r.matchValue);
    if (re && re.test(value)) return r.newValue as string;
  }

  // 3. template (regexp_replace with backrefs).
  for (const r of axisRules) {
    if (r.matchType !== "template") continue;
    const re = safeRegExp(r.matchValue);
    if (re && re.test(value)) {
      return value.replace(re, templateToJs(r.newValue as string));
    }
  }

  return null;
}
