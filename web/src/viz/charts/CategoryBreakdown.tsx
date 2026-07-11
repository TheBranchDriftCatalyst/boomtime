import { useMemo } from "react";
import { CHART_COLORS } from "@/lib/config";
import { secondsToHms } from "@/lib/utils";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { ResourceStats } from "@/types/api";

interface CategoryBreakdownProps {
  categories: ResourceStats[];
  height?: number;
}

/**
 * First-class category breakdown (Browsing / Coding / AI Coding / Meeting / …)
 * as clean horizontal bars with time + %. Makes clear that "tracked time" is
 * more than coding. Pure CSS/SVG-free bars — crisp at any width, dark-mode
 * native via theme tokens + the shared palette.
 */
export function CategoryBreakdown({ categories }: CategoryBreakdownProps) {
  const rows = useMemo(() => {
    const isOther = (n: string) => n.startsWith("Other (");
    const real = categories.filter(
      (c) => c.totalSeconds > 0 && !isOther(c.name),
    );
    const total = real.reduce((s, c) => s + c.totalSeconds, 0) || 1;
    return real
      .sort((a, b) => b.totalSeconds - a.totalSeconds)
      .map((c, i) => ({
        name: c.name,
        seconds: c.totalSeconds,
        pct: (c.totalSeconds / total) * 100,
        color: CHART_COLORS[i % CHART_COLORS.length],
      }));
  }, [categories]);

  if (rows.length === 0) return <EmptyChart height={160} />;

  const max = Math.max(...rows.map((r) => r.pct), 1);

  return (
    <div className="space-y-2.5">
      {rows.map((r) => (
        <div key={r.name} className="flex items-center gap-3 text-sm">
          <div className="flex w-32 shrink-0 items-center gap-2">
            <span
              className="h-3 w-3 shrink-0 rounded-sm"
              style={{ backgroundColor: r.color }}
            />
            <span className="truncate font-medium" title={r.name}>
              {r.name}
            </span>
          </div>
          <div className="h-2.5 flex-1 overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full"
              style={{
                width: `${(r.pct / max) * 100}%`,
                backgroundColor: r.color,
              }}
            />
          </div>
          <span className="w-24 shrink-0 text-right font-mono text-xs text-muted-foreground">
            {secondsToHms(r.seconds)}
          </span>
          <span className="w-12 shrink-0 text-right font-mono text-xs tabular-nums">
            {r.pct.toFixed(0)}%
          </span>
        </div>
      ))}
    </div>
  );
}
