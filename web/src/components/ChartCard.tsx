import type { ReactNode } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";

interface ChartCardProps {
  title: string;
  /**
   * Optional secondary line under the title — small, muted, sits inline
   * with the title. Used by per-axis charts (gaka-6ci) to disclose
   * excluded time ("28% untagged / browsing") so users see the gap
   * between the chart's aggregated total and their real tracked time.
   */
  subtitle?: ReactNode;
  action?: ReactNode;
  /**
   * Hover-revealed embed action(s) — pages pass an <EmbedLinkButton kind=…/>
   * (features/widgets) for charts that have a live server-rendered widget
   * twin. Charts without a twin show nothing until one exists (adding a twin
   * is a renderer in internal/widget + this one prop).
   */
  embedAction?: ReactNode;
  children: ReactNode;
}

export function ChartCard({ title, subtitle, action, embedAction, children }: ChartCardProps) {
  return (
    <Card className="group relative h-full" data-chart-card>
      <CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2">
        <div className="flex flex-col gap-0.5">
          <CardTitle className="text-sm font-semibold text-muted-foreground">
            {title}
          </CardTitle>
          {subtitle && (
            <div className="text-[10px] uppercase tracking-wider text-muted-foreground/70">
              {subtitle}
            </div>
          )}
        </div>
        {action}
      </CardHeader>
      {embedAction && (
        <div className="absolute right-2 top-2 z-10 flex gap-1 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100 sm:group-focus-within:opacity-100">
          {embedAction}
        </div>
      )}
      <CardContent>{children}</CardContent>
    </Card>
  );
}
