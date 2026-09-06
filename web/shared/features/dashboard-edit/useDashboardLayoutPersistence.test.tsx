// useDashboardLayoutPersistence.test.tsx (gaka-lzr Phase 6) — the DB
// load/debounced-save boundary layered onto useDashboardEditStore. Exercises
// the hook against the REAL store reducer (not a mock) + msw-mocked
// GET/PUT /api/v1/users/current/dashboard/:scope, using real timers (the
// 600ms save-debounce is short enough that waiting it out for real is more
// robust here than fighting fake-timer/msw-promise interleaving).
import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import type { GridLayoutItem } from "@shared/lib/grid";
import { moveResize, useDashboardEditStore } from "./useDashboardEditStore";
import { useDashboardLayoutPersistence } from "./useDashboardLayoutPersistence";

const DASHBOARD_URL = "/api/v1/users/current/dashboard/overview";

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

function makeQC() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
}

const DEFAULT_LAYOUT: GridLayoutItem[] = [{ i: "a", x: 0, y: 0, w: 6, h: 3 }];
const SAVED_LAYOUT: GridLayoutItem[] = [{ i: "b", x: 0, y: 0, w: 4, h: 2 }];

/** Combinator hook: the real edit store + the persistence hook, exposing both
 * so tests can dispatch edits and assert on the store's resulting state. */
function useHarness(scope: string, enabled = true) {
  const store = useDashboardEditStore(DEFAULT_LAYOUT);
  const persistence = useDashboardLayoutPersistence(
    scope,
    {
      state: store.state,
      dispatch: store.dispatch,
      isDirty: store.isDirty,
      markSaved: store.markSaved,
    },
    DEFAULT_LAYOUT,
    { enabled },
  );
  return { store, persistence };
}

function mockGet404() {
  server.use(
    http.get(DASHBOARD_URL, () => HttpResponse.json({ error: "not found" }, { status: 404 })),
  );
}

describe("useDashboardLayoutPersistence", () => {
  it("falls back to defaultLayout when GET 404s (no saved layout yet)", async () => {
    mockGet404();
    const { result } = renderHook(() => useHarness("overview"), { wrapper: wrapper(makeQC()) });

    await waitFor(() => expect(result.current.persistence.isHydrating).toBe(false));
    expect(result.current.store.state.layout).toEqual(DEFAULT_LAYOUT);
    expect(result.current.store.isDirty).toBe(false);
  });

  it("hydrates the store from a saved layout envelope on mount", async () => {
    server.use(
      http.get(DASHBOARD_URL, () =>
        HttpResponse.json({ layout: { cols: 12, widgets: SAVED_LAYOUT } }),
      ),
    );
    const { result } = renderHook(() => useHarness("overview"), { wrapper: wrapper(makeQC()) });

    await waitFor(() => expect(result.current.store.state.layout).toEqual(SAVED_LAYOUT));
    expect(result.current.store.isDirty).toBe(false);
    expect(result.current.store.canUndo).toBe(false); // hydrate resets history
  });

  it("autosaves a dirty edit (debounced) and clears the store's dirty flag on success", async () => {
    mockGet404();
    let putBody: { layout?: { widgets?: GridLayoutItem[] } } | null = null;
    server.use(
      http.put(DASHBOARD_URL, async ({ request }) => {
        putBody = (await request.json()) as { layout?: { widgets?: GridLayoutItem[] } };
        return HttpResponse.json(putBody);
      }),
    );
    const { result } = renderHook(() => useHarness("overview"), { wrapper: wrapper(makeQC()) });
    await waitFor(() => expect(result.current.persistence.isHydrating).toBe(false));

    act(() => result.current.store.dispatch(moveResize(SAVED_LAYOUT)));
    expect(result.current.store.isDirty).toBe(true);
    expect(putBody).toBeNull(); // not yet — still inside the debounce window

    await waitFor(() => expect(putBody).not.toBeNull(), { timeout: 2000 });
    expect(putBody!.layout!.widgets).toEqual(SAVED_LAYOUT);

    // markSaved() fires only after the PUT resolves — the dirty indicator
    // clears once the debounced save actually lands.
    await waitFor(() => expect(result.current.store.isDirty).toBe(false), { timeout: 2000 });
  });

  it("never issues the GET (or a PUT) while disabled", async () => {
    let getHit = false;
    server.use(
      http.get(DASHBOARD_URL, () => {
        getHit = true;
        return HttpResponse.json({ error: "not found" }, { status: 404 });
      }),
    );
    const { result } = renderHook(() => useHarness("overview", false), { wrapper: wrapper(makeQC()) });

    // Give any (incorrect) in-flight request time to land.
    await new Promise((r) => setTimeout(r, 200));
    expect(getHit).toBe(false);
    // The store stays at its OWN seed (never hydrated) while disabled.
    expect(result.current.store.state.layout).toEqual(DEFAULT_LAYOUT);
  });
});

// --- boom-28vm: the debounce's three holes ----------------------------------
// (1) an edit made <600ms before navigating away died with the cleared timer,
// (2) a failed PUT sat dirty until the user happened to edit again, and
// (3) two debounce cycles could race, letting a stalled OLDER PUT land after
//     the newer one — server on layout A, editor showing "saved" layout B.
describe("useDashboardLayoutPersistence — durability of the debounced save", () => {
  const EDIT_A: GridLayoutItem[] = [{ i: "a", x: 1, y: 0, w: 6, h: 3 }];
  const EDIT_B: GridLayoutItem[] = [{ i: "a", x: 5, y: 2, w: 4, h: 2 }];

  it("flushes the pending edit on unmount instead of dropping it", async () => {
    mockGet404();
    const puts: GridLayoutItem[][] = [];
    server.use(
      http.put(DASHBOARD_URL, async ({ request }) => {
        const body = (await request.json()) as { layout: { widgets: GridLayoutItem[] } };
        puts.push(body.layout.widgets);
        return HttpResponse.json(body);
      }),
    );
    const { result, unmount } = renderHook(() => useHarness("overview"), {
      wrapper: wrapper(makeQC()),
    });
    await waitFor(() => expect(result.current.persistence.isHydrating).toBe(false));

    act(() => result.current.store.dispatch(moveResize(EDIT_A)));
    // Navigate away INSIDE the 600ms debounce window — the user clicks a
    // sidebar link right after dragging a tile.
    unmount();

    await waitFor(() => expect(puts).toHaveLength(1), { timeout: 3000 });
    expect(puts[0]).toEqual(EDIT_A);
  });

  it("retries a failed autosave without waiting for another edit", async () => {
    mockGet404();
    let attempts = 0;
    server.use(
      http.put(DASHBOARD_URL, async ({ request }) => {
        attempts += 1;
        const body = (await request.json()) as { layout: { widgets: GridLayoutItem[] } };
        // First attempt: transient 5xx. The store must not stay stuck dirty.
        if (attempts === 1) return HttpResponse.json({ error: "boom" }, { status: 503 });
        return HttpResponse.json(body);
      }),
    );
    const { result } = renderHook(() => useHarness("overview"), {
      wrapper: wrapper(makeQC()),
    });
    await waitFor(() => expect(result.current.persistence.isHydrating).toBe(false));

    act(() => result.current.store.dispatch(moveResize(EDIT_A)));

    await waitFor(() => expect(attempts).toBeGreaterThanOrEqual(2), { timeout: 5000 });
    await waitFor(() => expect(result.current.store.isDirty).toBe(false), { timeout: 5000 });
  }, 15000);

  it("serializes PUTs so a stalled older write can never land after a newer one", async () => {
    mockGet404();
    const landed: GridLayoutItem[][] = [];
    let seen = 0;
    server.use(
      http.put(DASHBOARD_URL, async ({ request }) => {
        const body = (await request.json()) as { layout: { widgets: GridLayoutItem[] } };
        seen += 1;
        // The FIRST write stalls (slow connection / server GC) long enough
        // that, unserialized, the second write would commit before it.
        if (seen === 1) await new Promise((r) => setTimeout(r, 900));
        landed.push(body.layout.widgets);
        return HttpResponse.json(body);
      }),
    );
    const { result } = renderHook(() => useHarness("overview"), {
      wrapper: wrapper(makeQC()),
    });
    await waitFor(() => expect(result.current.persistence.isHydrating).toBe(false));

    act(() => result.current.store.dispatch(moveResize(EDIT_A)));
    // Let edit A's debounce fire, then make edit B while A is still in flight.
    await new Promise((r) => setTimeout(r, 750));
    act(() => result.current.store.dispatch(moveResize(EDIT_B)));

    await waitFor(() => expect(landed).toHaveLength(2), { timeout: 8000 });
    // The LAST thing the server committed must be the LAST thing the user did.
    expect(landed[landed.length - 1]).toEqual(EDIT_B);
    expect(landed).toEqual([EDIT_A, EDIT_B]);
  }, 20000);
});
