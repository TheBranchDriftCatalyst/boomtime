import { Layers } from "lucide-react";
import { secondsToHms } from "@/lib/utils";
import { shortPath } from "@/lib/pathLabel";
import { cn } from "@/lib/utils";
import type { CrossProjectFile } from "@/types/api";

interface CrossProjectFilesTableProps {
  files: CrossProjectFile[];
  truncated?: boolean;
  // Cap the rows rendered (backend already orders lynchpins-first).
  limit?: number;
}

/**
 * Compact table of the most-active files across ALL projects. Files touching
 * more than one project are cross-project "lynchpins" (shared interfaces /
 * comm channels) and are visually emphasized. Dark/synthwave-native via theme
 * tokens. Pure markup — crisp at any width, no freeze risk.
 */
export function CrossProjectFilesTable({
  files,
  truncated,
  limit = 20,
}: CrossProjectFilesTableProps) {
  const rows = files.slice(0, limit);

  if (rows.length === 0) {
    return (
      <p className="py-6 text-center text-sm text-muted-foreground">
        No file activity in this range.
      </p>
    );
  }

  return (
    <div>
      <div className="overflow-hidden rounded-md border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-xs text-muted-foreground">
            <tr>
              <th className="px-3 py-2 text-left font-medium">File</th>
              <th className="px-3 py-2 text-right font-medium">Time</th>
              <th className="px-3 py-2 text-right font-medium">Projects</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((f) => {
              const lynchpin = f.projects > 1;
              return (
                <tr
                  key={f.entity}
                  className={cn(
                    "border-t",
                    lynchpin
                      ? "bg-primary/5 hover:bg-primary/10"
                      : "hover:bg-muted/40",
                  )}
                >
                  <td className="max-w-0 px-3 py-1.5">
                    <span
                      className="block truncate font-mono text-xs"
                      title={f.entity}
                    >
                      {shortPath(f.entity)}
                    </span>
                  </td>
                  <td className="whitespace-nowrap px-3 py-1.5 text-right font-mono text-xs text-muted-foreground">
                    {secondsToHms(f.seconds)}
                  </td>
                  <td className="px-3 py-1.5 text-right">
                    {lynchpin ? (
                      <span
                        className="inline-flex items-center gap-1 rounded-full border border-primary/40 bg-primary/10 px-2 py-0.5 font-mono text-xs font-medium text-primary"
                        title={`Touches ${f.projects} projects — a cross-project lynchpin`}
                      >
                        <Layers className="h-3 w-3" />
                        {f.projects}
                      </span>
                    ) : (
                      <span className="font-mono text-xs text-muted-foreground">
                        {f.projects}
                      </span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {truncated && (
        <p className="mt-1.5 text-xs text-muted-foreground">
          Showing the top files only.
        </p>
      )}
    </div>
  );
}
