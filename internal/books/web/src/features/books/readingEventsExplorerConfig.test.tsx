// readingEventsExplorerConfig tests — the readingEvents adapter for
// <GroupableExplorer> (gaka-z5dz). runQuery is mocked so we assert the adapter's
// mapping in isolation:
//   - fetchGroup issues a grouped `reads` query (domain readingEvents) and maps
//     GroupRow → {value, stats:{count}} ("" → null); reads has no rollups, so the
//     group's count is the measure value.
//   - fetchLeaf issues the DSL `rows` mode and casts the payload to
//     ReadingEventRowDTO.
//   - the tab filters (source/origin/status/search) + drill path fold into `where`.
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { QueryResult, QuerySpec } from "@shared/lib/queryApi";
import type { ReadingEventRowDTO } from "@shared/types/meta";

const { runQueryMock } = vi.hoisted(() => ({ runQueryMock: vi.fn() }));
vi.mock("@shared/lib/queryApi", () => ({ runQuery: runQueryMock }));

// Imported AFTER the mock is registered.
import {
  buildWhere,
  filtersToPredicate,
  makeReadingEventsExplorerConfig,
  NO_EVENT_FILTERS,
  pathToPredicate,
  READING_EVENT_AXES,
  searchToPredicate,
  type ReadingEventsFilters,
} from "@books/features/books/readingEventsExplorerConfig";

beforeEach(() => {
  runQueryMock.mockReset();
});

describe("axes", () => {
  it("offers the event group axes (origin is the events-only axis)", () => {
    const ids = READING_EVENT_AXES.map((a) => a.id);
    expect(ids).toEqual(["origin", "source", "series", "author", "genre", "status"]);
  });
});

describe("pathToPredicate / filtersToPredicate / buildWhere", () => {
  it("maps a drill path to AND-ed eq leaves, skipping null steps", () => {
    expect(pathToPredicate([])).toBeUndefined();
    expect(pathToPredicate([{ dim: "origin", value: "audible" }])).toEqual({
      kind: "leaf",
      dim: "origin",
      op: "eq",
      values: ["audible"],
    });
    expect(
      pathToPredicate([
        { dim: "origin", value: "hardcover" },
        { dim: "author", value: "Sanderson" },
      ]),
    ).toEqual({
      kind: "and",
      of: [
        { kind: "leaf", dim: "origin", op: "eq", values: ["hardcover"] },
        { kind: "leaf", dim: "author", op: "eq", values: ["Sanderson"] },
      ],
    });
  });

  it("folds source / origin / status filters onto their eq leaves", () => {
    expect(filtersToPredicate(NO_EVENT_FILTERS)).toEqual([]);
    expect(
      filtersToPredicate({
        source: "audible",
        origin: "hardcover",
        status: "read",
        search: "",
      }),
    ).toEqual([
      { kind: "leaf", dim: "source", op: "eq", values: ["audible"] },
      { kind: "leaf", dim: "origin", op: "eq", values: ["hardcover"] },
      { kind: "leaf", dim: "status", op: "eq", values: ["read"] },
    ]);
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

  it("combines a drill path + origin filter + search into one AND-ed where", () => {
    expect(
      buildWhere([{ dim: "series", value: "Stormlight" }], {
        source: "all",
        origin: "audible",
        status: "all",
        search: "kings",
      }),
    ).toEqual({
      kind: "and",
      of: [
        { kind: "leaf", dim: "series", op: "eq", values: ["Stormlight"] },
        { kind: "leaf", dim: "origin", op: "eq", values: ["audible"] },
        {
          kind: "or",
          of: [
            { kind: "leaf", dim: "title", op: "ilike", values: ["kings"] },
            { kind: "leaf", dim: "author", op: "ilike", values: ["kings"] },
          ],
        },
      ],
    });
    expect(buildWhere([], NO_EVENT_FILTERS)).toBeUndefined();
  });
});

describe("source.fetchGroup", () => {
  it("runs a grouped reads query and maps rows to {value, stats:{count}}", async () => {
    runQueryMock.mockResolvedValue({
      kind: "groups",
      groups: [
        { key: "audible", value: 12, count: 12, stats: { count: 12 } },
        // Empty key = the null dimension value → mapped to value: null. No rollups,
        // so the count falls back to the measure value.
        { key: "", value: 3 },
      ],
    } satisfies QueryResult);

    const cfg = makeReadingEventsExplorerConfig({
      source: "audible",
      origin: "all",
      status: "all",
      search: "",
    });
    const page = await cfg.source.fetchGroup(
      [{ dim: "series", value: "Stormlight" }],
      "origin",
      [],
    );

    expect(page.groups).toEqual([
      { value: "audible", stats: { count: 12 } },
      { value: null, stats: { count: 3 } },
    ]);
    expect(page.truncated).toBe(false);

    const spec = runQueryMock.mock.calls[0][0] as QuerySpec;
    expect(spec).toMatchObject({
      domain: "readingEvents",
      measure: "reads",
      group: "origin",
      sort: { field: "value", desc: true },
    });
    // where folds the drill (series) + the source filter.
    expect(spec.where).toEqual({
      kind: "and",
      of: [
        { kind: "leaf", dim: "series", op: "eq", values: ["Stormlight"] },
        { kind: "leaf", dim: "source", op: "eq", values: ["audible"] },
      ],
    });
  });
});

describe("source.fetchLeaf", () => {
  const evt = (p: Partial<ReadingEventRowDTO>): Record<string, unknown> => ({
    origin: "audible",
    source: "audible",
    externalId: "B1",
    title: "Untitled",
    authors: "",
    status: "read",
    finishedAt: "2026-08-10T00:00:00Z",
    ...p,
  });

  it("runs the DSL rows mode and casts the payload to ReadingEventRowDTO", async () => {
    runQueryMock.mockResolvedValue({
      kind: "rows",
      total: 3,
      rows: [
        evt({ title: "The Way of Kings", authors: "Brandon Sanderson", origin: "audible" }),
        evt({ title: "The Way of Kings", authors: "Brandon Sanderson", origin: "hardcover", source: "" }),
      ],
    } satisfies QueryResult);

    const cfg = makeReadingEventsExplorerConfig({
      source: "all",
      origin: "all",
      status: "read",
      search: "",
    });
    const res = await cfg.source.fetchLeaf(
      [{ dim: "author", value: "Sanderson" }],
      1,
      250,
    );

    expect(res.total).toBe(3);
    expect(res.page).toBe(1);
    expect(res.limit).toBe(250);
    const first = res.rows[0] as ReadingEventRowDTO;
    expect(first.title).toBe("The Way of Kings");
    expect(res.rows[1].origin).toBe("hardcover");

    const spec = runQueryMock.mock.calls[0][0] as QuerySpec;
    expect(spec).toMatchObject({
      domain: "readingEvents",
      rows: true,
      page: { number: 1, size: 250 },
    });
    // where folds the drill path + the status=read filter.
    expect(spec.where).toEqual({
      kind: "and",
      of: [
        { kind: "leaf", dim: "author", op: "eq", values: ["Sanderson"] },
        { kind: "leaf", dim: "status", op: "eq", values: ["read"] },
      ],
    });
  });
});

// A tiny type-level guard that the filters shape is what the tab passes.
const _filters: ReadingEventsFilters = NO_EVENT_FILTERS;
void _filters;
