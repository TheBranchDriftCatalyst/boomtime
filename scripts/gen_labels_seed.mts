// gen_labels_seed.mts — one-shot generator that reads the TS LABEL_CATALOG,
// expands ...tierLabels(...) via the actual helper, and emits SQL INSERT
// statements for migration 00036_labels_catalog.sql.
//
// Run with:  node --experimental-strip-types scripts/gen_labels_seed.mts
//
// Prints SQL to stdout — pipe into the migration file (between the
// `-- SEED BEGIN` and `-- SEED END` markers, replacing whatever is between
// them). Regeneration is IDEMPOTENT: same TS input → same SQL output. This
// script is committed alongside the migration so the exact byte-for-byte
// derivation is reviewable.
//
// The migration is the source of truth once merged; this generator only
// exists to bootstrap the seed cleanly from the pre-DB TypeScript catalog.
//
// Deliberately NOT wired into CI / build. If the TS catalog changes AFTER
// the DB pivot ships, the migration is still frozen — new labels come in via
// the admin CRUD UI (or a fresh migration), not via re-running this script.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { LABEL_CATALOG } from "../web/src/features/publicprofile/labels/catalog.ts";
import type { LabelSpec } from "../web/src/features/publicprofile/labels/types.ts";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const manifestPath = path.join(
  __dirname,
  "..",
  "web",
  "src",
  "features",
  "publicprofile",
  "labels",
  "prompts.manifest.json"
);

interface Manifest {
  systemPrompt: string;
  labels: Record<string, { description: string; optimizedPrompt: string }>;
}

const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8")) as Manifest;

function sqlLit(s: string | undefined | null): string {
  if (s === undefined || s === null) return "NULL";
  return "'" + s.replace(/'/g, "''") + "'";
}

function jsonbLit(obj: unknown): string {
  const j = JSON.stringify(obj);
  return "'" + j.replace(/'/g, "''") + "'::jsonb";
}

const rows: string[] = [];
const seen = new Set<string>();
let matched = 0;
let unmatched = 0;
for (const s of LABEL_CATALOG as LabelSpec[]) {
  if (seen.has(s.id)) throw new Error(`duplicate id ${s.id}`);
  seen.add(s.id);
  const m = manifest.labels[s.id];
  let description = s.description;
  let optimized = s.imagePrompt ?? "";
  if (m) {
    description = m.description;
    optimized = m.optimizedPrompt;
    matched++;
  } else {
    unmatched++;
    process.stderr.write(`  (no manifest entry for ${s.id} — using TS description + imagePrompt)\n`);
  }
  const cols = [
    sqlLit(s.id),
    sqlLit(s.kind),
    sqlLit(s.label),
    sqlLit(s.glyph),
    sqlLit(description),
    sqlLit(optimized || null),
    String(s.rank),
    sqlLit(s.tier),
    jsonbLit(s.condition),
  ];
  rows.push(`  (${cols.join(", ")})`);
}

process.stdout.write(
  "INSERT INTO labels (id, kind, label, glyph, description, optimized_prompt, rank, tier, condition) VALUES\n"
);
process.stdout.write(rows.join(",\n"));
process.stdout.write(";\n");
process.stdout.write("\n-- label_gen_config singleton — global systemPrompt.\n");
process.stdout.write(
  `UPDATE label_gen_config SET system_prompt = ${sqlLit(
    manifest.systemPrompt
  )}, updated_at = now() WHERE singleton = true;\n`
);
process.stderr.write(
  `emitted ${rows.length} label rows (manifest matched: ${matched}, fallback: ${unmatched}); systemPrompt len=${manifest.systemPrompt.length}\n`
);
