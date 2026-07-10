import { useMemo } from "react";
import { CHART_COLORS } from "@/lib/config";
import { secondsToHms } from "@/lib/utils";
import type { ResourceStats } from "@/types/api";

interface TopProjectsBarProps {
  projects: ResourceStats[];
  topN?: number;
  // Optional: clicking a bar selects that project in the per-project panel.
  onSelect?: (project: string) => void;
}

const isOther = (n: string) => n === "Other" || n.startsWith("Other (");

/**
 * Compact horizontal-bar breakdown of the top projects (by tracked time), using
 * the shared chart palette. CSS bars — crisp at any width, dark-mode native.
 * Bars are clickable to drill into the per-project panel below.
 */
export function TopProjectsBar({
  projects,
  topN = 8,
  onSelect,
}: TopProjectsBarProps) {
  const rows = useMemo(() => {
    const real = projects.filter((p) => p.totalSeconds > 0 && !isOther(p.name));
    return real
      .sort((a, b) => b.totalSeconds - a.totalSeconds)
      .slice(0, topN)
      .map((p, i) => ({
        name: p.name,
        seconds: p.totalSeconds,
        color: CHART_COLORS[i % CHART_COLORS.length],
      }));
  }, [projects, topN]);

  if (rows.length === 0) {
    return (
      <p className="py-6 text-center text-sm text-muted-foreground">
        No project activity in this range.
      </p>
    );
  }

  const max = Math.max(...rows.map((r) => r.seconds), 1);

  return (
    <div className="space-y-2.5">
      {rows.map((r) => {
        const Row = onSelect ? "button" : "div";
        return (
          <Row
            key={r.name}
            {...(onSelect
              ? {
                  type: "button" as const,
                  onClick: () => onSelect(r.name),
                  title: `View ${r.name} detail`,
                }
              : {})}
            className={
              "flex w-full items-center gap-3 rounded text-left text-sm" +
              (onSelect ? " cursor-pointer hover:bg-muted/50" : "")
            }
          >
            <span
              className="w-40 shrink-0 truncate font-medium"
              title={r.name}
            >
              {r.name}
            </span>
            <div className="h-2.5 flex-1 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full"
                style={{
                  width: `${(r.seconds / max) * 100}%`,
                  backgroundColor: r.color,
                }}
              />
            </div>
            <span className="w-28 shrink-0 text-right font-mono text-xs text-muted-foreground">
              {secondsToHms(r.seconds)}
            </span>
          </Row>
        );
      })}
    </div>
  );
}
