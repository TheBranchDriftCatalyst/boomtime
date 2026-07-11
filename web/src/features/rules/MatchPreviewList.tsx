import { Fragment, type ReactNode } from "react";

/** Shared preview box chrome (border + muted background). */
export function MatchPreviewContainer({ children }: { children: ReactNode }) {
  return (
    <div className="space-y-1 rounded-md border bg-background/60 p-2">
      {children}
    </div>
  );
}

export interface PreviewValueRow {
  value: string;
  count: number;
}

interface MatchPreviewListProps<R> {
  /** The "Matches N value(s)" total line. */
  title: ReactNode;
  rows: readonly R[];
  /**
   * Shown when rows is empty; pass null to suppress (e.g. while the preview is
   * still loading, or for variants without an empty state).
   */
  emptyText?: string | null;
  /** Custom row renderer (e.g. template-mode raw → mapped rows). */
  renderRow?: (row: R) => ReactNode;
  rowKey?: (row: R) => string;
}

function defaultRow(row: PreviewValueRow): ReactNode {
  return (
    <div className="flex items-center justify-between gap-2 text-xs">
      <span className="truncate font-mono" title={row.value}>
        {row.value}
      </span>
      <span className="shrink-0 tabular-nums text-muted-foreground">
        {row.count.toLocaleString()}
      </span>
    </div>
  );
}

/**
 * Shared "what does this rule match" preview: a total line plus sample rows of
 * {value, count} (or a custom renderer), with an optional empty-state message.
 * Used by RemappingForm's client-side preview and SpaceRuleForm's server-side
 * preview so both render the identical block.
 */
export function MatchPreviewList<R>({
  title,
  rows,
  emptyText = "No values match yet.",
  renderRow,
  rowKey,
}: MatchPreviewListProps<R>) {
  const render =
    renderRow ?? (defaultRow as unknown as (row: R) => ReactNode);
  const key =
    rowKey ?? ((row: R) => (row as unknown as PreviewValueRow).value);
  return (
    <MatchPreviewContainer>
      <p className="text-xs font-medium text-muted-foreground">{title}</p>
      {rows.map((row) => (
        <Fragment key={key(row)}>{render(row)}</Fragment>
      ))}
      {emptyText != null && rows.length === 0 && (
        <p className="text-xs text-muted-foreground">{emptyText}</p>
      )}
    </MatchPreviewContainer>
  );
}
