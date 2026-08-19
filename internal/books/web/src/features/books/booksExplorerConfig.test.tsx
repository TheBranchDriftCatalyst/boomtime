// booksExplorerConfig tests — the reading-domain adapter for <GroupableExplorer>
// (gaka-02sh Track C). runQuery is mocked so we assert the adapter's mapping in
// isolation:
//   - fetchGroup issues a grouped `books` query and maps GroupRow → {value,stats}
//     (count + runtime + finished; "" → null; "Other" preserved).
//   - fetchLeaf issues the DSL `rows` mode and casts the payload to ReadingItemDTO,
//     applying the client-side search filter the DSL can't express.
//   - the page filters (source/status) + drill path fold into every `where`.
//   - deriveHeroStats sums the source-grouped hero query.
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { QueryResult, QuerySpec } from "@shared/lib/queryApi";
import type { ReadingItemDTO } from "@shared/types/meta";

const { runQueryMock } = vi.hoisted(() => ({ runQueryMock: vi.fn() }));
vi.mock("@shared/lib/queryApi", () => ({ runQuery: runQueryMock }));

// Imported AFTER the mock is registered.
import {
  buildWhere,
  deriveHeroStats,
  deriveMatchStats,
  filtersToPredicate,
  HERO_SPEC,
  makeBooksExplorerConfig,
  makeHeroSpec,
  pathToPredicate,
  READING_AXES,
  searchToPredicate,
  STATUS_FILTER_OPTIONS,
  type BooksFilters,
} from "@books/features/books/booksExplorerConfig";

const NO_FILTERS: BooksFilters = { source: "all", status: "all",
      matched: "all", search: "" };

beforeEach(() => {
  runQueryMock.mockReset();
});

describe("pathToPredicate / filtersToPredicate / buildWhere", () => {
  it("maps a drill path to AND-ed eq leaves, skipping null steps", () => {
    expect(pathToPredicate([])).toBeUndefined();
    expect(
      pathToPredicate([{ dim: "author", value: "Sanderson" }]),
    ).toEqual({ kind: "leaf", dim: "author", op: "eq", values: ["Sanderson"] });
    expect(
      pathToPredicate([
        { dim: "author", value: "Sanderson" },
        { dim: "series", value: "Stormlight" },
      ]),
    ).toEqual({
      kind: "and",
      of: [
        { kind: "leaf", dim: "author", op: "eq", values: ["Sanderson"] },
        { kind: "leaf", dim: "series", op: "eq", values: ["Stormlight"] },
      ],
    });
  });

  it("folds source + status filters onto the canonical status value (1:1)", () => {
    expect(filtersToPredicate(NO_FILTERS)).toEqual([]);
    // gaka-books: the filter value IS the canonical column value now — `read`
    // filters on status='read' directly (no more "finished" → "read" remap).
    expect(
      filtersToPredicate({ source: "audible", status: "read",
      matched: "all", search: "" }),
    ).toEqual([
      { kind: "leaf", dim: "source", op: "eq", values: ["audible"] },
      { kind: "leaf", dim: "status", op: "eq", values: ["read"] },
    ]);
    expect(
      filtersToPredicate({ source: "all", status: "reading",
      matched: "all", search: "" }),
    ).toEqual([{ kind: "leaf", dim: "status", op: "eq", values: ["reading"] }]);
    // The new paused/dnf statuses fold the same way — they only exist via
    // curation overrides, but the filter speaks them 1:1.
    expect(
      filtersToPredicate({ source: "all", status: "dnf",
      matched: "all", search: "" }),
    ).toEqual([{ kind: "leaf", dim: "status", op: "eq", values: ["dnf"] }]);
  });

  it("folds the Hardcover match-state (meta-status) filter onto the isMatched dim", () => {
    expect(
      filtersToPredicate({ source: "all", status: "all",
      matched: "matched", search: "" }),
    ).toEqual([{ kind: "leaf", dim: "isMatched", op: "eq", values: ["matched"] }]);
    expect(
      filtersToPredicate({ source: "all", status: "all",
      matched: "unmatched", search: "" }),
    ).toEqual([{ kind: "leaf", dim: "isMatched", op: "eq", values: ["unmatched"] }]);
    // matched:"all" contributes no predicate.
    expect(
      filtersToPredicate({ source: "all", status: "all",
      matched: "all", search: "" }),
    ).toEqual([]);
    // "Out of sync" (diverged) folds onto the finer syncState dim, not isMatched.
    expect(
      filtersToPredicate({ source: "all", status: "all",
      matched: "diverged", search: "" }),
    ).toEqual([{ kind: "leaf", dim: "syncState", op: "eq", values: ["diverged"] }]);
  });

  it("offers the canonical status filter set (no stray 'Finished' mislabel value)", () => {
    // filter VALUES are exactly all + the 5 canonical statuses (== group values
    // == pill keys). "finished" is gone as a VALUE; `read`'s label is "Finished".
    expect(STATUS_FILTER_OPTIONS.map((o) => o.value)).toEqual([
      "all",
      "want",
      "reading",
      "read",
      "paused",
      "dnf",
    ]);
    const read = STATUS_FILTER_OPTIONS.find((o) => o.value === "read");
    expect(read?.label).toBe("Finished");
    // Grouping exposes BOTH the effective status axis and the raw Amazon one.
    expect(READING_AXES.map((a) => a.id)).toContain("status");
    // statusDerived ("Status (Amazon)") was demoted from the default axes — status
    // is 1:1 with the Hardcover shelf now (status consolidation).
    expect(READING_AXES.map((a) => a.id)).not.toContain("statusDerived");
  });

  it("maps a search term to an ILIKE OR on title/author (trimmed; blank → none)", () => {
    expect(searchToPredicate("")).toBeUndefined();
    expect(searchToPredicate("   ")).toBeUndefined();
    expect(searchToPredicate("  dune ")).toEqual({
      kind: "or",
      of: [
        { kind: "leaf", dim: "title", op: "ilike", values: ["dune"] },
        { kind: "leaf", dim: "author", op: "ilike", values: ["dune"] },
      ],
    });
  });

  it("folds search into filtersToPredicate as the ILIKE OR node", () => {
    expect(
      filtersToPredicate({ source: "kindle", status: "all",
      matched: "all", search: "weir" }),
    ).toEqual([
      { kind: "leaf", dim: "source", op: "eq", values: ["kindle"] },
      {
        kind: "or",
        of: [
          { kind: "leaf", dim: "title", op: "ilike", values: ["weir"] },
          { kind: "leaf", dim: "author", op: "ilike", values: ["weir"] },
        ],
      },
    ]);
  });

  it("combines a drill path + status + search into one AND-ed where", () => {
    expect(
      buildWhere([{ dim: "series", value: "Dune" }], {
        source: "all",
        status: "reading",
      matched: "all",
        search: "messiah",
      }),
    ).toEqual({
      kind: "and",
      of: [
        { kind: "leaf", dim: "series", op: "eq", values: ["Dune"] },
        { kind: "leaf", dim: "status", op: "eq", values: ["reading"] },
        {
          kind: "or",
          of: [
            { kind: "leaf", dim: "title", op: "ilike", values: ["messiah"] },
            { kind: "leaf", dim: "author", op: "ilike", values: ["messiah"] },
          ],
        },
      ],
    });
  });

  it("combines a path + filters into one where predicate", () => {
    expect(
      buildWhere([{ dim: "author", value: "Weir" }], {
        source: "kindle",
        status: "all",
      matched: "all",
        search: "",
      }),
    ).toEqual({
      kind: "and",
      of: [
        { kind: "leaf", dim: "author", op: "eq", values: ["Weir"] },
        { kind: "leaf", dim: "source", op: "eq", values: ["kindle"] },
      ],
    });
    expect(buildWhere([], NO_FILTERS)).toBeUndefined();
  });
});

describe("source.fetchGroup", () => {
  it("runs a grouped books query and maps rows to {value,stats}", async () => {
    runQueryMock.mockResolvedValue({
      kind: "groups",
      groups: [
        {
          key: "Brandon Sanderson",
          value: 12,
          count: 12,
          stats: { count: 12, runtime: 1320, finished: 5 },
        },
        // Empty key = the null dimension value → mapped to value: null.
        { key: "", value: 3, count: 3, stats: { count: 3, runtime: 60, finished: 0 } },
        { key: "Other", value: 20, count: 20, stats: { count: 20, runtime: 0, finished: 2 } },
      ],
    } satisfies QueryResult);

    const cfg = makeBooksExplorerConfig({
      source: "audible",
      status: "all",
      matched: "all",
      search: "",
    });
    const page = await cfg.source.fetchGroup(
      [{ dim: "series", value: "Cosmere" }],
      "author",
      ["runtime", "finished"],
    );

    // Mapping: value + full stats; "" → null; "Other" preserved.
    expect(page.groups).toEqual([
      { value: "Brandon Sanderson", stats: { count: 12, runtime: 1320, finished: 5 } },
      { value: null, stats: { count: 3, runtime: 60, finished: 0 } },
      { value: "Other", stats: { count: 20, runtime: 0, finished: 2 } },
    ]);
    expect(page.truncated).toBe(false);

    // The spec carried the axis, the requested rollups, and the folded where
    // (drill series + source filter). No bucket: Books returns all real groups —
    // no synthetic 'Other' catch-all (gaka-a6nc).
    const spec = runQueryMock.mock.calls[0][0] as QuerySpec;
    expect(spec).toMatchObject({
      domain: "reading",
      measure: "books",
      group: "author",
      rollups: ["runtime", "finished"],
      sort: { field: "value", desc: true },
    });
    expect(spec.bucket).toBeUndefined();
    expect(spec.where).toEqual({
      kind: "and",
      of: [
        { kind: "leaf", dim: "series", op: "eq", values: ["Cosmere"] },
        { kind: "leaf", dim: "source", op: "eq", values: ["audible"] },
      ],
    });
  });

  it("folds search into the grouped query's where (constrains aggregates)", async () => {
    runQueryMock.mockResolvedValue({ kind: "groups", groups: [] } satisfies QueryResult);

    const cfg = makeBooksExplorerConfig({
      source: "all",
      status: "all",
      matched: "all",
      search: "dune",
    });
    await cfg.source.fetchGroup([], "author", ["runtime", "finished"]);

    // The group aggregate is constrained by the SAME ILIKE OR — search reaches
    // the group counts, not just the fetched leaf page.
    const spec = runQueryMock.mock.calls[0][0] as QuerySpec;
    expect(spec.where).toEqual({
      kind: "or",
      of: [
        { kind: "leaf", dim: "title", op: "ilike", values: ["dune"] },
        { kind: "leaf", dim: "author", op: "ilike", values: ["dune"] },
      ],
    });
  });
});

describe("source.fetchLeaf", () => {
  const dto = (p: Partial<ReadingItemDTO>): Record<string, unknown> => ({
    source: "audible",
    externalId: "B1",
    title: "Untitled",
    authors: "",
    status: "reading",
    progressPercent: 0,
    finished: false,
    syncedAt: "2026-08-01T00:00:00Z",
    ...p,
  });

  it("runs the DSL rows mode and casts the payload to ReadingItemDTO", async () => {
    runQueryMock.mockResolvedValue({
      kind: "rows",
      rows: [
        dto({ title: "Project Hail Mary", authors: "Andy Weir", externalId: "B08" }),
        dto({ title: "Dune", authors: "Frank Herbert", externalId: "B09" }),
      ],
      total: 42,
    } satisfies QueryResult);

    const cfg = makeBooksExplorerConfig({
      source: "all",
      status: "reading",
      matched: "all",
      search: "",
    });
    const res = await cfg.source.fetchLeaf(
      [{ dim: "author", value: "Weir" }],
      1,
      250,
    );

    expect(res.total).toBe(42);
    expect(res.page).toBe(1);
    expect(res.limit).toBe(250);
    // Rows are the raw DTO shape, directly usable by the columns.
    const first = res.rows[0] as ReadingItemDTO;
    expect(first.title).toBe("Project Hail Mary");
    expect(first.externalId).toBe("B08");

    const spec = runQueryMock.mock.calls[0][0] as QuerySpec;
    expect(spec).toMatchObject({
      domain: "reading",
      rows: true,
      page: { number: 1, size: 250 },
    });
    // where folds the drill path + the status=reading filter.
    expect(spec.where).toEqual({
      kind: "and",
      of: [
        { kind: "leaf", dim: "author", op: "eq", values: ["Weir"] },
        { kind: "leaf", dim: "status", op: "eq", values: ["reading"] },
      ],
    });
  });

  it("folds search into the server-side where (no client-side filtering)", async () => {
    // The backend applies the ILIKE, so the mock's payload passes through
    // verbatim — fetchLeaf trusts the server total and does NOT re-filter.
    runQueryMock.mockResolvedValue({
      kind: "rows",
      rows: [dto({ title: "Project Hail Mary", authors: "Andy Weir" })],
      total: 1,
    } satisfies QueryResult);

    const cfg = makeBooksExplorerConfig({
      source: "all",
      status: "all",
      matched: "all",
      search: "weir",
    });
    const res = await cfg.source.fetchLeaf([], 1, 250);

    // Rows + total pass through untouched (server already constrained them).
    expect(res.rows).toHaveLength(1);
    expect(res.total).toBe(1);

    // The search rode into spec.where as the ILIKE OR on title/author.
    const spec = runQueryMock.mock.calls[0][0] as QuerySpec;
    expect(spec.where).toEqual({
      kind: "or",
      of: [
        { kind: "leaf", dim: "title", op: "ilike", values: ["weir"] },
        { kind: "leaf", dim: "author", op: "ilike", values: ["weir"] },
      ],
    });
  });
});

describe("deriveHeroStats", () => {
  it("sums the source-grouped hero query into the per-source hero counts", () => {
    const stats = deriveHeroStats([
      { key: "audible", value: 10, count: 10, stats: { count: 10, finished: 6 } },
      { key: "kindle", value: 4, count: 4, stats: { count: 4, finished: 1 } },
      // hardcover source — shelved-but-not-owned books.
      { key: "hardcover", value: 5, count: 5, stats: { count: 5, finished: 2 } },
      { key: "", value: 2, count: 2, stats: { count: 2, finished: 0 } },
    ]);
    expect(stats).toEqual({
      total: 21,
      finished: 9,
      audible: 10,
      kindle: 4,
      hardcover: 5,
      // matched/unmatched come from the sibling isMatched query, not this one.
      matched: 0,
      unmatched: 0,
    });
  });

  it("sums matched/unmatched from an isMatched-grouped result", () => {
    expect(
      deriveMatchStats([
        { key: "matched", value: 812, count: 812 },
        { key: "unmatched", value: 457, count: 457 },
      ]),
    ).toEqual({ matched: 812, unmatched: 457 });
  });
});

describe("makeHeroSpec", () => {
  const NO_FILTERS_SPEC: BooksFilters = { source: "all", status: "all",
      matched: "all", search: "" };

  it("returns the bare hero spec (no where) when no filter is active", () => {
    expect(makeHeroSpec(NO_FILTERS_SPEC)).toEqual({
      ...HERO_SPEC,
      where: undefined,
    });
  });

  it("folds the active source filter into the hero spec's where", () => {
    // The filter-scoped hero uses the SAME source/status/search predicates the
    // explorer folds — so `<filtered>/<total>` counts share one predicate path.
    expect(
      makeHeroSpec({ source: "hardcover", status: "all",
      matched: "all", search: "" }).where,
    ).toEqual({ kind: "leaf", dim: "source", op: "eq", values: ["hardcover"] });
  });
});
