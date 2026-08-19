// Pure filter/creatable logic for the Combobox, extracted so it can be unit
// tested without rendering. The component re-uses these directly.

interface FilterableOption {
  value: string;
}

/** Cap so a huge option list never renders thousands of rows. */
export const COMBOBOX_MAX_RESULTS = 200;

/** Case-insensitive substring filter, capped at COMBOBOX_MAX_RESULTS. */
export function filterOptions<T extends FilterableOption>(
  options: T[],
  search: string,
): T[] {
  const q = search.trim().toLowerCase();
  const list = q
    ? options.filter((o) => o.value.toLowerCase().includes(q))
    : options;
  return list.slice(0, COMBOBOX_MAX_RESULTS);
}

/** Whether an exact (case-insensitive) match for `search` already exists. */
export function exactExists(
  options: FilterableOption[],
  search: string,
): boolean {
  const q = search.trim().toLowerCase();
  return options.some((o) => o.value.toLowerCase() === q);
}

/**
 * Whether the "Use \"...\"" create affordance should show: creatable mode is on,
 * the search is non-empty, and it isn't already an exact option.
 */
export function canCreate(
  options: FilterableOption[],
  search: string,
  creatable: boolean,
): boolean {
  return creatable && search.trim().length > 0 && !exactExists(options, search);
}
