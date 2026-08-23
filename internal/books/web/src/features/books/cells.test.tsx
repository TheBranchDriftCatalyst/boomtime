// cells.test.tsx — the editable Books curation cells (boom-books Stage 5).
// api.setBookCuration is spied so we assert the wiring in isolation: the
// StatusSelect dropdown offers the 5 canonical statuses, selecting one fires an
// optimistic PATCH with the right body (and the pill flips before the request
// resolves), the provenance dot reflects override vs auto-derived, and the
// rating / finished editors fire their own patches.
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { api } from "@shared/lib/api";
import { BOOK_STATUSES, type ReadingItemDTO } from "@shared/types/meta";
import {
  FinishedEditor,
  RatingEditor,
  SourceBadge,
  statusProvenance,
  StatusSelect,
} from "./cells";

const baseItem = (p: Partial<ReadingItemDTO> = {}): ReadingItemDTO => ({
  source: "kindle",
  externalId: "B08GB58KD5",
  title: "Project Hail Mary",
  authors: "Andy Weir",
  status: "reading",
  progressPercent: 40,
  finished: false,
  syncedAt: "2026-08-01T00:00:00Z",
  ...p,
});

// A never-resolving promise so we can observe the OPTIMISTIC UI before settle.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

afterEach(() => vi.restoreAllMocks());

describe("SourceBadge", () => {
  it("renders a distinct pill per source (audible / kindle / hardcover)", () => {
    const { rerender } = renderWithProviders(<SourceBadge source="audible" />);
    expect(screen.getByText("Audible")).toBeInTheDocument();

    rerender(<SourceBadge source="kindle" />);
    expect(screen.getByText("Kindle")).toBeInTheDocument();

    // The new hardcover source gets its own fuchsia pill (the Hardcover accent).
    rerender(<SourceBadge source="hardcover" />);
    const hc = screen.getByText("Hardcover");
    expect(hc).toBeInTheDocument();
    expect(hc.closest("span")?.className).toMatch(/fuchsia/);
  });
});

describe("StatusSelect", () => {
  it("offers the 5 canonical statuses in the dropdown", async () => {
    vi.spyOn(api, "setBookCuration").mockResolvedValue(baseItem());
    renderWithProviders(<StatusSelect item={baseItem()} />);

    await userEvent.click(
      screen.getByRole("button", { name: /Change status/ }),
    );
    const menu = await screen.findByRole("menu");
    // Every canonical label is present — want/reading/read(→Finished)/paused/dnf.
    for (const label of ["Want", "Reading", "Finished", "Paused", "DNF"]) {
      expect(within(menu).getByText(label)).toBeInTheDocument();
    }
    // Exactly 5 menu items (one per canonical status).
    expect(within(menu).getAllByRole("menuitem")).toHaveLength(
      BOOK_STATUSES.length,
    );
  });

  it("fires setBookCuration optimistically with the picked status", async () => {
    const d = deferred<ReadingItemDTO>();
    const spy = vi
      .spyOn(api, "setBookCuration")
      .mockReturnValue(d.promise);
    const item = baseItem({ status: "reading" });
    renderWithProviders(<StatusSelect item={item} />);

    const trigger = screen.getByRole("button", { name: /Change status/ });
    expect(trigger).toHaveTextContent("Reading");

    await userEvent.click(trigger);
    await userEvent.click(
      within(await screen.findByRole("menu")).getByText("DNF"),
    );

    // The PATCH carried only the status key, with the canonical value.
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy.mock.calls[0][1]).toEqual({ status: "dnf" });
    expect(spy.mock.calls[0][0]).toMatchObject({
      source: "kindle",
      externalId: "B08GB58KD5",
    });

    // Optimistic: the pill flips to DNF before the request resolves.
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Change status/ }),
      ).toHaveTextContent("DNF"),
    );

    d.resolve(item); // let the mutation settle
  });

  it("rolls back the pill when the PATCH fails", async () => {
    const d = deferred<ReadingItemDTO>();
    vi.spyOn(api, "setBookCuration").mockReturnValue(d.promise);
    renderWithProviders(<StatusSelect item={baseItem({ status: "reading" })} />);

    await userEvent.click(
      screen.getByRole("button", { name: /Change status/ }),
    );
    await userEvent.click(
      within(await screen.findByRole("menu")).getByText("Paused"),
    );
    // Optimistically Paused…
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Change status/ }),
      ).toHaveTextContent("Paused"),
    );

    d.reject(new Error("boom"));
    // …then reverts to Reading on error.
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Change status/ }),
      ).toHaveTextContent("Reading"),
    );
  });

  it("shows a provenance dot for a diverging override and none for auto-derived", () => {
    // Auto-derived (no override): no provenance dot.
    const { unmount } = renderWithProviders(
      <StatusSelect item={baseItem({ statusIsOverride: false })} />,
    );
    expect(screen.queryByLabelText(/override|Hardcover/i)).toBeNull();
    unmount();

    // A genuine curation (override diverges from the Amazon-derived layer):
    // marked a 'reading' book 'dnf' → the "Curated override" dot is present.
    renderWithProviders(
      <StatusSelect
        item={baseItem({
          status: "dnf",
          statusIsOverride: true,
          statusOverride: "dnf",
          statusDerived: "reading",
        })}
      />,
    );
    expect(
      screen.getByLabelText("Curated override — pushed to Hardcover"),
    ).toBeInTheDocument();
  });
});

describe("statusProvenance", () => {
  it("classifies auto / curated / hardcover", () => {
    // No override at all → auto.
    expect(statusProvenance(baseItem({ statusIsOverride: false }))).toBe("auto");

    // A NATURAL FINISH: the backend stamps override='read' to advance the LWW
    // clock, but it equals the derived layer ('read') → NOT a real curation, so
    // no dot even though statusIsOverride is true.
    expect(
      statusProvenance(
        baseItem({
          status: "read",
          statusIsOverride: true,
          statusOverride: "read",
          statusDerived: "read",
        }),
      ),
    ).toBe("auto");

    // A genuine user curation: reading → dnf (override diverges from derived).
    expect(
      statusProvenance(
        baseItem({
          status: "dnf",
          statusIsOverride: true,
          statusOverride: "dnf",
          statusDerived: "reading",
          hardcoverStatus: "reading",
        }),
      ),
    ).toBe("curated");

    // A diverging override that matches the last-seen Hardcover shelf → adopted
    // FROM Hardcover.
    expect(
      statusProvenance(
        baseItem({
          status: "read",
          statusIsOverride: true,
          statusOverride: "read",
          statusDerived: "reading",
          hardcoverStatus: "read",
        }),
      ),
    ).toBe("hardcover");

    // A pending optimistic pick always reads as the user's own edit.
    expect(
      statusProvenance(baseItem({ statusIsOverride: false }), "paused"),
    ).toBe("curated");
  });
});

describe("RatingEditor", () => {
  it("fires setBookCuration with the picked rating", async () => {
    const spy = vi
      .spyOn(api, "setBookCuration")
      .mockResolvedValue(baseItem({ rating: 4 }));
    renderWithProviders(<RatingEditor item={baseItem({ rating: 0 })} />);

    await userEvent.click(screen.getByRole("radio", { name: "4 stars" }));

    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy.mock.calls[0][1]).toEqual({ rating: 4 });
  });

  it("clears the rating override when the current star is clicked again", async () => {
    const spy = vi
      .spyOn(api, "setBookCuration")
      .mockResolvedValue(baseItem({ rating: 0 }));
    renderWithProviders(<RatingEditor item={baseItem({ rating: 3 })} />);

    // Clicking the already-selected 3rd star clears (rating: null).
    await userEvent.click(screen.getByRole("radio", { name: "3 stars" }));
    expect(spy.mock.calls[0][1]).toEqual({ rating: null });
  });
});

describe("FinishedEditor", () => {
  it("clears the finished-date override via the popover", async () => {
    const spy = vi
      .spyOn(api, "setBookCuration")
      .mockResolvedValue(baseItem({ finishedAt: undefined }));
    renderWithProviders(
      <FinishedEditor item={baseItem({ finishedAt: "2026-07-04T00:00:00Z" })} />,
    );

    await userEvent.click(screen.getByRole("button", { name: /Finished/ }));
    await userEvent.click(
      await screen.findByText("Clear finished date"),
    );

    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy.mock.calls[0][1]).toEqual({ finishedAt: null });
  });
});
