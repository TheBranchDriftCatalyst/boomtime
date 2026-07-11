import { useState } from "react";
import { ChevronDown, ChevronRight, TriangleAlert } from "lucide-react";
import { cn } from "@/lib/utils";
import type { DriftFinding } from "@/types/api";

interface DriftBannerProps {
  findings: DriftFinding[] | null | undefined;
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

  if (!findings || findings.length === 0) return null;

  const hasError = findings.some((f) => f.severity === "error");
  const totalCount = findings.reduce((sum, f) => sum + (f.count ?? 1), 0);

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
      )}
    </div>
  );
}
