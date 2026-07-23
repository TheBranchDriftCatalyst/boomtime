import type { ReactNode } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";

interface ChartCardProps {
  title: string;
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

export function ChartCard({ title, action, embedAction, children }: ChartCardProps) {
  return (
    <Card className="group relative h-full" data-chart-card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-semibold text-muted-foreground">
          {title}
        </CardTitle>
        {action}
      </CardHeader>
      {embedAction && (
        <div className="absolute right-2 top-2 z-10 flex gap-1 opacity-0 transition-opacity group-hover:opacity-100">
          {embedAction}
        </div>
      )}
      <CardContent>{children}</CardContent>
    </Card>
  );
}
