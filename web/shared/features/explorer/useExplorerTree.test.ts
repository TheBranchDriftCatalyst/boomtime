// useExplorerTree.test.ts — the tree's response/inputs race (boom-28vm audit).
//
// Changing the date range (or the axes) wipes the caches and refires the root
// query, but nothing used to stop the PREVIOUS query's in-flight promise: a
// slow first response landing after the fast new one repainted the tree with
// the old range's groups under the new range's header, and the only way out was
// forcing another reload.
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { DomainConfig, GroupPage, LeafResult, TreeSource } from "./types";
import type { GroupNode } from "./explorerModel";
import { useExplorerTree } from "./useExplorerTree";

interface Row {
  id: string;
}

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function groupPage(values: string[]): GroupPage {
  return {
    groups: values.map((v) => ({ value: v, stats: { count: 1 } })),
    truncated: false,
  };
}

const EMPTY_LEAF: LeafResult<Row> = { rows: [], total: 0, page: 1, limit: 50 };

/** A source whose group fetches are resolved by the test, in order. */
function makeConfig() {
  const groupCalls: Deferred<GroupPage>[] = [];
  const source: TreeSource<Row> = {
    fetchGroup: () => {
      const d = deferred<GroupPage>();
      groupCalls.push(d);
      return d.promise;
    },
    fetchLeaf: () => Promise.resolve(EMPTY_LEAF),
  };
  const config: DomainConfig<Row> = {
    axes: [{ id: "project", label: "Project" }],
    defaultGroupBy: ["project"],
    columns: [],
    rollups: [],
    source,
    rowKey: (r) => r.id,
    leafPageSize: 50,
    labels: { leafGroup: "Rows" },
  };
  return { config, groupCalls };
}

function values(nodes: ReturnType<typeof useExplorerTree<Row>>["tree"]): (string | null)[] {
  return nodes.map((n) => (n as GroupNode).value);
}

describe("useExplorerTree — stale responses after an inputs change", () => {
  it("discards a root response from the PREVIOUS resetKey", async () => {
    const { config, groupCalls } = makeConfig();
    const { result, rerender } = renderHook(
      (props: { resetKey: string }) =>
        useExplorerTree<Row>({
          config,
          axes: ["project"],
          resetKey: props.resetKey,
          flatWhenEmpty: false,
        }),
      { initialProps: { resetKey: "range-1" } },
    );

    await waitFor(() => expect(groupCalls).toHaveLength(1));

    // User changes the date range while the first query is still in flight.
    rerender({ resetKey: "range-2" });
    await waitFor(() => expect(groupCalls).toHaveLength(2));

    // The NEW query answers first and renders.
    await act(async () => {
      groupCalls[1].resolve(groupPage(["new-range-project"]));
    });
    await waitFor(() => expect(values(result.current.tree)).toEqual(["new-range-project"]));

    // The OLD query finally answers. It must be dropped on the floor — before
    // the fix it overwrote the tree with the previous range's groups.
    await act(async () => {
      groupCalls[0].resolve(groupPage(["old-range-project"]));
    });
    expect(values(result.current.tree)).toEqual(["new-range-project"]);
  });

  it("discards a FAILURE from the previous resetKey (no phantom error state)", async () => {
    const { config, groupCalls } = makeConfig();
    const { result, rerender } = renderHook(
      (props: { resetKey: string }) =>
        useExplorerTree<Row>({
          config,
          axes: ["project"],
          resetKey: props.resetKey,
          flatWhenEmpty: false,
        }),
      { initialProps: { resetKey: "range-1" } },
    );
    await waitFor(() => expect(groupCalls).toHaveLength(1));
    rerender({ resetKey: "range-2" });
    await waitFor(() => expect(groupCalls).toHaveLength(2));

    await act(async () => {
      groupCalls[1].resolve(groupPage(["fresh"]));
    });
    await waitFor(() => expect(values(result.current.tree)).toEqual(["fresh"]));

    await act(async () => {
      groupCalls[0].reject(new Error("previous range timed out"));
    });
    expect(result.current.rootError).toBe(false);
    expect(values(result.current.tree)).toEqual(["fresh"]);
  });

  it("still renders the response for the CURRENT inputs (guard isn't over-eager)", async () => {
    const { config, groupCalls } = makeConfig();
    const { result } = renderHook(() =>
      useExplorerTree<Row>({
        config,
        axes: ["project"],
        resetKey: "range-1",
        flatWhenEmpty: false,
      }),
    );
    await waitFor(() => expect(groupCalls).toHaveLength(1));
    await act(async () => {
      groupCalls[0].resolve(groupPage(["a", "b"]));
    });
    await waitFor(() => expect(values(result.current.tree)).toEqual(["a", "b"]));
    expect(result.current.rootLoading).toBe(false);
  });
});
