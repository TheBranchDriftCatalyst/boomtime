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
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { GridLayoutItem } from "@/lib/grid";
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
