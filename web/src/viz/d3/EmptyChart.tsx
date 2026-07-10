/** Shared "No data available" placeholder. */
export function EmptyChart({ height }: { height: number }) {
  return (
    <div
      className="flex items-center justify-center text-sm text-muted-foreground"
      style={{ height }}
    >
      No data available
    </div>
  );
}
