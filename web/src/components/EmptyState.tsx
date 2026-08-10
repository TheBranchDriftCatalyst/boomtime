import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

interface EmptyStateProps {
  /** Lucide glyph anchoring the state — sits in an accent-tinted tile. */
  icon: LucideIcon;
  /** Short headline, e.g. "No projects yet". */
  title: string;
  /** One-line explanation of why the surface is empty + what to do. */
  description?: ReactNode;
  /** Optional CTA — typically a <Button asChild><Link/></Button> or a plain
   *  <Button onClick/>. */
  action?: ReactNode;
  className?: string;
}

/**
 * Shared, on-brand empty state (gaka-gbbl.2): an accent-tinted icon tile, a
 * headline, a muted one-liner, and an optional CTA. Card-less by design — the
 * caller wraps it in a <Card>/<CardContent> when the surrounding surface needs
 * a panel, or drops it straight into an existing card body. Mirrors the
 * dossier/synthwave chrome already used by the GitHub + Wellness empty cards so
 * every empty surface reads as one system.
 */
export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        "flex flex-col items-center gap-3 py-10 text-center",
        className,
      )}
    >
      <span className="flex h-12 w-12 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/20">
        <Icon className="h-6 w-6" />
      </span>
      <div className="space-y-1">
        <h3 className="text-sm font-semibold">{title}</h3>
        {description && (
          <p className="mx-auto max-w-sm text-sm text-muted-foreground">
            {description}
          </p>
        )}
      </div>
      {action && <div className="pt-1">{action}</div>}
    </div>
  );
}
