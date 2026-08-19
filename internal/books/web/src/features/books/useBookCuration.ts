// useBookCuration — the react-query binding for per-book curation overrides
// (gaka-books Stage 5). A "curation" write is a user (or Hardcover-adopted)
// override of the status / rating / finished-date that maps to Hardcover, sent
// to PATCH /api/v1/books/items/:id/curation. The endpoint returns the updated
// EFFECTIVE reading row (override ?? derived), which the caller folds back into
// the cell so the pill/stars/date stay correct after the write.
//
// The Books explorer table is NOT react-query backed (it runs on its own
// useExplorerTree cache), so the immediate visual is carried by cell-local
// optimistic state; this hook's job is the network write + rollback signal +
// invalidating the react-query surfaces that DO derive from reading state (the
// page hero counts + any grouped reading/books charts) so they refetch.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { CurationPatch, ReadingItemDTO } from "@shared/types/meta";

// Grouped-query cache prefixes whose results derive from reading state. Mirrors
// usePins: invalidate them so a status change (a book leaving the "reading" set,
// a fresh finish) is reflected in any open grouped reading/books chart.
const READING_QUERY_PREFIX = ["reading-query"] as const;
const BOOKS_EXPLORE_PREFIX = ["books-explore"] as const;

/**
 * A mutation that pushes a curation override for ONE reading row. `mutateAsync`
 * resolves to the updated effective DTO; callers seed cell-local optimistic
 * state before calling and roll back in the mutation's `onError`.
 */
export function useSetBookCuration(item: ReadingItemDTO) {
  const qc = useQueryClient();
  return useMutation<ReadingItemDTO, Error, CurationPatch>({
    mutationFn: (patch) => api.setBookCuration(item, patch),
    onSuccess: () => {
      // The library hero counts (Tracked / Finished) and any grouped reading
      // charts re-derive from the row we just changed — refetch them. The cell
      // itself self-updates via cell-local optimistic state (no table refetch).
      qc.invalidateQueries({ queryKey: qk.booksHero() });
      qc.invalidateQueries({ queryKey: READING_QUERY_PREFIX });
      qc.invalidateQueries({ queryKey: BOOKS_EXPLORE_PREFIX });
    },
  });
}
