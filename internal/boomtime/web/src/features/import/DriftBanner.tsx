import { useState } from "react";
import {
  ChevronDown,
  ChevronRight,
  Clipboard,
  ClipboardCheck,
  Table as TableIcon,
  TriangleAlert,
} from "lucide-react";
import { toast } from "sonner";
import { cn, copyToClipboard } from "@/lib/utils";
import type { DriftFinding } from "@/types/api";

interface DriftBannerProps {
  findings: DriftFinding[] | null | undefined;
}

// Copy-payload shape produced by the "Copy for schema update" button. Kept
// self-describing so a downstream LLM or API only needs the pasted blob to
// know what it's looking at + what to do with it.
const COPY_INSTRUCTIONS =
  "Extend the wakatime.com schema definitions in internal/importer/drift.go " +
  "to typed-handle each finding below (unknown_field -> add to the schema; " +
  "missing_required -> mark optional or update decoder; type_changed -> retype). " +
  "Where a new field carries analytic value, add a matching column/rollup and " +
  "wire a graph or badge on the FE. Preserve backwards compatibility on decode.";

function buildCopyPayload(findings: DriftFinding[]): string {
  const payload = {
    source: "boomtime import drift",
    capturedAt: new Date().toISOString(),
    instructions: COPY_INSTRUCTIONS,
    findings,
  };
  return JSON.stringify(payload, null, 2);
}

function buildMarkdownTable(findings: DriftFinding[]): string {
  const rows = findings.map(
    (f) =>
      `| \`${f.endpoint}\` | \`${f.field || "-"}\` | ${f.kind} | ${
        f.detail || "-"
      } | ${f.severity} | ${f.firstSeenDay || "-"} | ${f.count} |`,
  );
  return [
    "**wakatime.com API drift** (from boomtime import)",
    "",
    "| Endpoint | Field | Kind | Detail | Severity | First seen | Count |",
    "| --- | --- | --- | --- | --- | --- | --- |",
    ...rows,
  ].join("\n");
}

/**
 * gaka-unq.1: warning banner shown on the Import page when the wakatime.com API
 * schema has drifted during a run (unknown/missing/type-changed fields, or a
 * broken envelope). Amber when only warnings; red-tinted when any finding has
 * severity=error. Collapsible detail table.
 *
 * Import proceeds through most drift (matching backend semantics: warn, don't
 * fail); the banner exists so users know which fields may have been dropped.
 */
export function DriftBanner({ findings }: DriftBannerProps) {
  const [open, setOpen] = useState(false);
  const [justCopied, setJustCopied] = useState<null | "json" | "markdown">(
    null,
  );

  if (!findings || findings.length === 0) return null;

  const hasError = findings.some((f) => f.severity === "error");
  const totalCount = findings.reduce((sum, f) => sum + (f.count ?? 1), 0);

  async function copy(
    kind: "json" | "markdown",
    build: (f: DriftFinding[]) => string,
    label: string,
  ) {
    await copyToClipboard(build(findings ?? []));
    toast.success(`${label} copied to clipboard`);
    setJustCopied(kind);
    window.setTimeout(() => setJustCopied(null), 1500);
  }

  return (
    <div
      role="alert"
      className={cn(
        "rounded-md border p-3 text-sm",
        hasError
          ? "border-destructive/40 bg-destructive/10 text-destructive"
          : "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400",
      )}
    >
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-start gap-2 text-left"
      >
        <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" />
        <div className="flex-1">
          <div className="font-medium">
            wakatime.com API drift detected — {findings.length} finding
            {findings.length === 1 ? "" : "s"}
            {totalCount !== findings.length ? ` (${totalCount} events)` : ""}
            {hasError
              ? "; some rows may not have been imported"
              : "; some fields may not have been imported"}
          </div>
          <div className="text-xs opacity-80">
            {open ? "Hide details" : "Show details"}
          </div>
        </div>
        {open ? (
          <ChevronDown className="mt-0.5 h-4 w-4 shrink-0" />
        ) : (
          <ChevronRight className="mt-0.5 h-4 w-4 shrink-0" />
        )}
      </button>

      {open && (
        <>
          <div className="mt-3 overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-current/20 text-left opacity-70">
                  <th className="py-1 pr-3 font-medium">Endpoint</th>
                  <th className="py-1 pr-3 font-medium">Field</th>
                  <th className="py-1 pr-3 font-medium">Kind</th>
                  <th className="py-1 pr-3 font-medium">Detail</th>
                  <th className="py-1 pr-3 font-medium">Severity</th>
                  <th className="py-1 pr-3 font-medium">First seen</th>
                  <th className="py-1 pr-3 text-right font-medium">Count</th>
                </tr>
              </thead>
              <tbody>
                {findings.map((f, i) => (
                  <tr key={i} className="border-b border-current/10 last:border-0">
                    <td className="py-1 pr-3 font-mono">{f.endpoint}</td>
                    <td className="py-1 pr-3 font-mono">{f.field || "-"}</td>
                    <td className="py-1 pr-3">{f.kind}</td>
                    <td className="py-1 pr-3">{f.detail || "-"}</td>
                    <td className="py-1 pr-3">{f.severity}</td>
                    <td className="py-1 pr-3">{f.firstSeenDay || "-"}</td>
                    <td className="py-1 pr-3 text-right">{f.count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* gaka-rl6: copy affordances for feeding findings back into a
              schema-update loop (LLM prompt, PR body, or a bespoke API). */}
          <div className="mt-3 flex flex-wrap items-center gap-2 text-xs">
            <span className="opacity-70">Feed these findings back:</span>
            <button
              type="button"
              onClick={() =>
                copy("json", buildCopyPayload, "Schema-update JSON")
              }
              className="inline-flex items-center gap-1 rounded border border-current/40 bg-current/5 px-2 py-1 font-medium hover:bg-current/10"
              title="Copy a self-describing JSON payload — includes an instructions header for a downstream LLM or API"
            >
              {justCopied === "json" ? (
                <ClipboardCheck className="h-3.5 w-3.5" />
              ) : (
                <Clipboard className="h-3.5 w-3.5" />
              )}
              Copy JSON (with instructions)
            </button>
            <button
              type="button"
              onClick={() =>
                copy("markdown", buildMarkdownTable, "Markdown table")
              }
              className="inline-flex items-center gap-1 rounded border border-current/40 bg-current/5 px-2 py-1 font-medium hover:bg-current/10"
              title="Copy a markdown table — good for pasting into a PR / chat / prompt"
            >
              {justCopied === "markdown" ? (
                <ClipboardCheck className="h-3.5 w-3.5" />
              ) : (
                <TableIcon className="h-3.5 w-3.5" />
              )}
              Copy as markdown
            </button>
          </div>
        </>
      )}
    </div>
  );
}
