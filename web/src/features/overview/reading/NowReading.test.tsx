// NowReading.test.tsx — the one Reading tile backed by the existing
// reading_items endpoint (not the DSL). Asserts it filters to status="reading",
// sorts by progress desc, and shows an empty state when nothing is in progress.
import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import { NowReadingTile } from "./NowReading";
import type { ReadingItemDTO } from "@/types/api";

function item(partial: Partial<ReadingItemDTO>): ReadingItemDTO {
  return {
    source: "audible",
    externalId: Math.random().toString(36).slice(2),
    title: "Untitled",
    authors: "",
    status: "reading",
    progressPercent: 0,
    finished: false,
    syncedAt: "2026-08-01T00:00:00Z",
    ...partial,
  };
}

function stubItems(items: ReadingItemDTO[]) {
  server.use(
    http.get("/api/v1/books/items", () => HttpResponse.json({ items })),
  );
}

describe("NowReadingTile", () => {
  it("lists only in-progress items, sorted by progress desc", async () => {
    stubItems([
      item({ title: "Half Way", status: "reading", progressPercent: 40 }),
      item({ title: "Almost Done", status: "reading", progressPercent: 80 }),
      item({ title: "Finished Book", status: "read", progressPercent: 100 }),
    ]);
    renderWithProviders(<NowReadingTile />);

    await screen.findByText("Almost Done");
    // The finished (status="read") book is filtered out.
    expect(screen.queryByText("Finished Book")).not.toBeInTheDocument();

    const rows = screen.getAllByTestId("now-reading-row");
    expect(rows).toHaveLength(2);
    // Sorted by progress desc → "Almost Done" (80%) first.
    expect(within(rows[0]).getByText("Almost Done")).toBeInTheDocument();
    expect(within(rows[0]).getByText("80%")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Half Way")).toBeInTheDocument();
  });

  it("shows an empty state when nothing is in progress", async () => {
    stubItems([item({ title: "Done", status: "read", progressPercent: 100 })]);
    renderWithProviders(<NowReadingTile />);
    expect(
      await screen.findByText("Nothing marked as currently reading."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("now-reading-list")).not.toBeInTheDocument();
  });
});
