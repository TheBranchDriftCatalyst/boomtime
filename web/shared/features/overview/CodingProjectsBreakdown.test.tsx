// CodingProjectsBreakdown.test.tsx — the Overview "Project breakdown" tile,
// migrated onto the query DSL so it honors canonical PINS (gaka-canon).
//
// Non-tautological anchors:
//   - The tile issues the EXACT coding DSL spec (coding·project·seconds, a
//     topN+Other bucket, bound to the passed range) — not just "some query".
//   - It maps the returned {key,value} SECONDS groups into the donut legend,
//     formatted as durations (secondsToHms), and a low-share project the
//     backend kept (because it is pinned) renders as its OWN legend row/slice,
//     never folded into "Other".
//   - The per-project pin toggle drives the REAL usePins → real api → MSW: a
//     click POSTs the canonical pin body with axis="project", and because the
//     tile's query is keyed under the ["curation"] prefix that usePins
//     invalidates, the pin REFETCHES the coding query (runQuery fires again).
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import { server } from "@shared/test/msw/server";
import {
  makeTestQueryClient,
  renderWithProviders,
} from "@shared/test/renderWithProviders";
import type { QueryResult } from "@shared/lib/queryApi";

// Mock the DSL client so no network/auth runs for the aggregate; react-query
// still drives the loading→data ladder through the real useCodingQuery hook.
// (usePins/api still hit MSW — nothing about pins is mocked.)
vi.mock("@shared/lib/queryApi", () => ({ runQuery: vi.fn() }));
import { runQuery } from "@shared/lib/queryApi";
import { CodingProjectsBreakdown } from "./CodingProjectsBreakdown";

const mockRun = vi.mocked(runQuery);

const RANGE = {
  startISO: "2026-07-01T00:00:00.000Z",
  endISO: "2026-08-01T00:00:00.000Z",
};

afterEach(() => {
  mockRun.mockReset();
});

/** Resolve every runQuery call to one fixed result. */
function resolveWith(result: QueryResult) {
  mockRun.mockResolvedValue(result);
}

describe("CodingProjectsBreakdown", () => {
  it("issues the coding·project·seconds DSL spec bound to the range (topN+Other)", async () => {
    resolveWith({ kind: "groups", groups: [{ key: "boomtime", value: 3600 }] });
    renderWithProviders(<CodingProjectsBreakdown {...RANGE} />);

    await waitFor(() =>
      expect(mockRun).toHaveBeenCalledWith(
        expect.objectContaining({
          domain: "coding",
          measure: "seconds",
          group: "project",
          bucket: expect.objectContaining({ other: true }),
          over: expect.objectContaining({
            range: { between: { start: RANGE.startISO, end: RANGE.endISO } },
          }),
        }),
      ),
    );
    // The bucket keeps a finite top-N before the tail collapses into "Other".
    const spec = mockRun.mock.calls[0][0];
    expect(spec.bucket?.topN).toBeGreaterThan(0);
  });

  it("maps the returned seconds groups into a formatted donut legend", async () => {
    resolveWith({
      kind: "groups",
      groups: [
        { key: "boomtime", value: 3 * 3600 + 12 * 60 }, // 3 hrs 12 mins
        { key: "catalyst-ui", value: 90 * 60 }, // 1 hr 30 mins
      ],
    });
    renderWithProviders(<CodingProjectsBreakdown {...RANGE} />);

    expect(await screen.findByText("boomtime")).toBeInTheDocument();
    const legend = screen.getByTestId("coding-projects-legend");
    expect(within(legend).getByText("catalyst-ui")).toBeInTheDocument();
    // Values are durations, not raw seconds (secondsToHms output).
    expect(legend).toHaveTextContent("3 hrs 12 mins");
    expect(legend).toHaveTextContent("1 hr 30 mins");
  });

  it("renders a pinned low-share project as its own slice — never inside Other", async () => {
    // The backend, honoring a pin on project=side-quest, KEEPS it as its own
    // group even at a tiny share; the tail still collapses into "Other".
    resolveWith({
      kind: "groups",
      groups: [
        { key: "boomtime", value: 10 * 3600 },
        { key: "catalyst-ui", value: 4 * 3600 },
        { key: "side-quest", value: 60 }, // 1 min — would normally be tail
        { key: "Other", value: 2 * 3600 },
      ],
    });
    renderWithProviders(<CodingProjectsBreakdown {...RANGE} />);

    const legend = await screen.findByTestId("coding-projects-legend");
    // The low-share project is its own legend row (with a canonize toggle)…
    const sideQuestRow = within(legend)
      .getByText("side-quest")
      .closest("li") as HTMLElement;
    expect(sideQuestRow).not.toBeNull();
    expect(within(sideQuestRow).getByTestId("pin-toggle")).toBeInTheDocument();
    // …and it is a real donut slice, distinct from the Other roll-up.
    const donut = screen.getByTestId("coding-projects-donut");
    expect(within(donut).getByText(/side-quest: 1 min/)).toBeInTheDocument();

    // The "Other" roll-up row exists but carries NO pin toggle (it is not a
    // real value), so side-quest is not "inside" Other.
    const otherRow = within(legend)
      .getByText("Other")
      .closest("li") as HTMLElement;
    expect(within(otherRow).queryByTestId("pin-toggle")).toBeNull();
  });

  it("pins a project via the curation API (axis='project') and refetches the coding query", async () => {
    resolveWith({
      kind: "groups",
      groups: [
        { key: "boomtime", value: 10 * 3600 },
        { key: "Other", value: 3600 },
      ],
    });

    // No pins yet → toggles render inactive.
    server.use(
      http.get("/api/v1/users/current/curation", () =>
        HttpResponse.json({ rules: [] }),
      ),
    );
    let posted: unknown;
    server.use(
      http.post("/api/v1/users/current/curation", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json({ rule: { id: 1, action: "pin" } });
      }),
    );

    const qc = makeTestQueryClient();
    renderWithProviders(<CodingProjectsBreakdown {...RANGE} />, {
      queryClient: qc,
    });

    const legend = await screen.findByTestId("coding-projects-legend");
    // Only the real project gets a toggle; "Other" does not.
    const toggles = within(legend).getAllByTestId("pin-toggle");
    expect(toggles).toHaveLength(1);

    // Let the initial load settle, then snapshot the call count as the baseline.
    await waitFor(() => expect(mockRun.mock.calls.length).toBeGreaterThan(0));
    const before = mockRun.mock.calls.length;

    await userEvent.click(toggles[0]);

    // The create call carries the canonical pin body on the "project" axis.
    await waitFor(() => expect(posted).toBeDefined());
    expect(posted).toMatchObject({
      axis: "project",
      action: "pin",
      matchType: "exact",
      matchValue: "boomtime",
    });

    // usePins invalidates the ["curation"] prefix this query is keyed under, so
    // the coding query REFETCHES — runQuery fires a second time. This is what
    // lets the freshly-pinned project escape "Other".
    await waitFor(() => expect(mockRun.mock.calls.length).toBeGreaterThan(before));
  });
});
