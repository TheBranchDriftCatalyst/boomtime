import {
  Card,
  CardContent,
  CardHeader,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { LoadingSkeleton } from "@thebranchdriftcatalyst/catalyst-ui/ui/loading-skeleton";
import { cn } from "@shared/lib/utils";

/**
 * Content-shaped loading skeletons (gaka-gbbl.2).
 *
 * Built on catalyst-ui's `LoadingSkeleton` pulse primitive (motion-safe
 * `animate-pulse`, `prefers-reduced-motion`-aware) composed into the shape of
 * the real dashboard surfaces. A route swap previews its eventual layout —
 * stat-tile grid, chart cards, table rows — instead of a bare centered
 * spinner, so the page feels like it's assembling rather than blank-then-pop.
 *
 * NOTE: `LoadingSkeleton`'s `width` prop is really an arbitrary class-append on
 * the inner pulse block (twMerge'd over the variant defaults), so we use it to
 * size individual bars/blocks; `className` lands on the outer wrapper.
 */

/** One StatCard-shaped tile: accent icon square + two stacked text bars. */
function StatTileSkeleton() {
  return (
    <Card>
      <CardContent className="flex items-center gap-4 p-5">
        <div className="h-12 w-12 shrink-0">
          <LoadingSkeleton variant="box" width="rounded-lg" />
        </div>
        <div className="min-w-0 flex-1 space-y-2">
          <LoadingSkeleton variant="line" width="h-2.5 w-20" />
          <LoadingSkeleton variant="line" width="h-4 w-28" />
        </div>
      </CardContent>
    </Card>
  );
}

/** The 1/2/4-col StatCard grid used atop Overview + Projects. */
export function StatTilesSkeleton({ count = 4 }: { count?: number }) {
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
      {Array.from({ length: count }).map((_, i) => (
        <StatTileSkeleton key={i} />
      ))}
    </div>
  );
}

/** A ChartCard placeholder: a title bar over a solid chart block. */
export function ChartCardSkeleton({
  heightClass = "h-40",
  className,
}: {
  heightClass?: string;
  className?: string;
}) {
  return (
    <Card className={cn("h-full", className)}>
      <CardHeader className="pb-2">
        <LoadingSkeleton variant="line" width="h-3 w-32" />
      </CardHeader>
      <CardContent>
        <LoadingSkeleton variant="card" width={cn("w-full", heightClass)} />
      </CardContent>
    </Card>
  );
}

/** Stacked table-row placeholders: rank bar, wide label, trailing value. */
export function TableRowsSkeleton({
  rows = 5,
  className,
}: {
  rows?: number;
  className?: string;
}) {
  return (
    <div className={cn("space-y-3", className)} aria-busy="true">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-3">
          <LoadingSkeleton variant="line" width="h-3 w-6" />
          <LoadingSkeleton variant="line" className="flex-1" width="h-3 w-full" />
          <LoadingSkeleton variant="line" width="h-3 w-14" />
        </div>
      ))}
    </div>
  );
}

/** Overview dashboard: stat tiles + a headline chart + a 2/1 chart row. */
export function OverviewSkeleton() {
  return (
    <div className="space-y-6">
      <StatTilesSkeleton />
      <ChartCardSkeleton heightClass="h-48" />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <ChartCardSkeleton className="lg:col-span-2" heightClass="h-56" />
        <ChartCardSkeleton heightClass="h-56" />
      </div>
    </div>
  );
}

/** Per-project detail: stat tiles + a 2/1 chart row + a split chart row. */
export function ProjectDetailSkeleton() {
  return (
    <div className="space-y-6">
      <StatTilesSkeleton />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <ChartCardSkeleton className="lg:col-span-2" heightClass="h-56" />
        <ChartCardSkeleton heightClass="h-56" />
      </div>
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <ChartCardSkeleton heightClass="h-40" />
        <ChartCardSkeleton heightClass="h-40" />
      </div>
    </div>
  );
}

/** Two side-by-side leaderboard cards, each a titled stack of rank rows. */
export function LeaderboardsSkeleton() {
  return (
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
      {[0, 1].map((i) => (
        <Card key={i}>
          <CardHeader className="pb-2">
            <LoadingSkeleton variant="line" width="h-3 w-28" />
          </CardHeader>
          <CardContent>
            <TableRowsSkeleton rows={6} />
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

/** Goal rows: a bordered card per goal with a name bar + progress bar. */
export function GoalsListSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div className="space-y-2" aria-busy="true">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="space-y-2 rounded-md border bg-secondary/40 p-3">
          <LoadingSkeleton variant="line" width="h-3.5 w-40" />
          <LoadingSkeleton variant="line" width="h-2 w-full" />
        </div>
      ))}
    </div>
  );
}
