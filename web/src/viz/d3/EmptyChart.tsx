/** Shared "No data available" placeholder.
 *
 * Optional `title` + `hint` lets a chart explain *why* the panel is empty
 * (e.g. range too short, no punchcard-eligible activity) and nudge the user
 * toward the fix (widen the range) instead of showing a bare grid.
 */
export function EmptyChart({
  height,
  title = "No data available",
  hint,
}: {
  height: number;
  title?: string;
  hint?: string;
}) {
  return (
    <div
      className="flex flex-col items-center justify-center gap-1 px-4 text-center text-sm text-muted-foreground"
      style={{ height }}
    >
      <span>{title}</span>
      {hint && <span className="text-xs opacity-80">{hint}</span>}
    </div>
  );
}
