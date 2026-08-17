// booksRefresh — a tiny context so a deep Books cell (status dropdown, rating,
// match-fixer) can force the explorer table to refetch after a mutation. The
// GroupableExplorer is NOT react-query backed (its own useExplorerTree cache is
// keyed on resetKey), so invalidating react-query alone doesn't refresh the rows
// (gaka-imeb). BooksPage bumps a version folded into the explorer's resetKey; the
// cells call refresh() in their mutation onSuccess. Default is a no-op so the same
// cell components work outside a Books page (e.g. the detail panel still fine).
import { createContext, useContext } from "react";

export const BooksRefreshContext = createContext<() => void>(() => {});

export function useBooksRefresh(): () => void {
  return useContext(BooksRefreshContext);
}
