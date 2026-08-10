import type { ReactNode } from "react";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";

/** The slice of a TanStack `useQuery` result the gate needs. */
interface GateableQuery<T> {
  data: T | undefined;
  isLoading: boolean;
  isError: boolean;
}

interface QueryGateProps<T> {
  query: GateableQuery<T>;
  /** Shown when the query fails, e.g. "Failed to load projects." */
  errorMessage: string;
  /**
   * Optional content-shaped placeholder rendered while loading (gaka-gbbl.2).
   * When omitted the gate falls back to the centered `<Spinner/>`, so every
   * existing caller is unchanged; heavy pages pass a `<…Skeleton/>` from
   * `@/components/Skeletons` that previews the eventual layout.
   */
  skeleton?: ReactNode;
  /** Rendered once data is available (type-narrowed to non-undefined). */
  children: (data: T) => ReactNode;
}

/**
 * Standard loading/error/data ladder for a query-backed section: a skeleton (or
 * spinner) while loading (or before data exists), a destructive-text message on
 * error, and the render-prop children once data arrives. Replaces the bare
 * `isLoading || !data ? <Spinner/> : …` pattern, which spins forever on a
 * failed query.
 */
export function QueryGate<T>({
  query,
  errorMessage,
  skeleton,
  children,
}: QueryGateProps<T>) {
  if (query.isError) {
    return (
      <p className="py-6 text-center text-sm text-destructive">{errorMessage}</p>
    );
  }
  if (query.isLoading || query.data === undefined) {
    return <>{skeleton ?? <Spinner />}</>;
  }
  return <>{children(query.data)}</>;
}
