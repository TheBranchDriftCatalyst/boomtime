import type { LucideIcon } from "lucide-react";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { cn } from "@/lib/utils";

interface StatCardProps {
  name: string;
  value: string | number;
  icon: LucideIcon;
  accent?: "primary" | "info" | "success" | "warning";
}

const accentMap = {
  primary: "text-primary bg-primary/10",
  info: "text-sky-500 bg-sky-500/10",
  success: "text-emerald-500 bg-emerald-500/10",
  warning: "text-amber-500 bg-amber-500/10",
};

export function StatCard({
  name,
  value,
  icon: Icon,
  accent = "primary",
}: StatCardProps) {
  return (
    <Card>
      <CardContent className="flex items-center gap-4 p-5">
        <div
          className={cn(
            "flex h-12 w-12 shrink-0 items-center justify-center rounded-lg",
            accentMap[accent],
          )}
        >
          <Icon className="h-6 w-6" />
        </div>
        <div className="min-w-0">
          <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            {name}
          </p>
          <p className="truncate text-lg font-bold" title={String(value)}>
            {value}
          </p>
        </div>
      </CardContent>
    </Card>
  );
}
