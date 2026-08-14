// NowReading.test.tsx — the one Reading tile backed by the existing
// reading_items endpoint (not the DSL). Asserts it filters to status="reading",
// sorts by progress desc, and shows an empty state when nothing is in progress.
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

afterEach(() => {
  vi.restoreAllMocks();
});

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

  it("excludes effectively-finished items above 95% even if still 'reading'", async () => {
    stubItems([
      item({ title: "Genuinely Mid", status: "reading", progressPercent: 60 }),
      item({ title: "Basically Done", status: "reading", progressPercent: 99 }),
      item({ title: "Truly Done", status: "reading", progressPercent: 100 }),
    ]);
    renderWithProviders(<NowReadingTile />);

    await screen.findByText("Genuinely Mid");
    expect(screen.queryByText("Basically Done")).not.toBeInTheDocument();
    expect(screen.queryByText("Truly Done")).not.toBeInTheDocument();
    expect(screen.getAllByTestId("now-reading-row")).toHaveLength(1);
  });

  it("shows the empty state when every in-progress item is >95%", async () => {
    stubItems([
      item({ title: "Basically Done", status: "reading", progressPercent: 99 }),
    ]);
    renderWithProviders(<NowReadingTile />);
    expect(
      await screen.findByText("Nothing marked as currently reading."),
    ).toBeInTheDocument();
  });

  it("opens an ASIN-precise Hardcover search in a new tab when a row is clicked", async () => {
    const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
    stubItems([
      item({
        title: "The Way of Kings",
        authors: "Brandon Sanderson",
        externalId: "B0041JKFJW", // the ASIN drives a precise search
        status: "reading",
        progressPercent: 42,
      }),
    ]);
    renderWithProviders(<NowReadingTile />);

    const row = await screen.findByTestId("now-reading-row");
    await userEvent.click(row);

    expect(openSpy).toHaveBeenCalledTimes(1);
    const [url, target, features] = openSpy.mock.calls[0];
    expect(url).toBe("https://hardcover.app/search?q=B0041JKFJW");
    expect(target).toBe("_blank");
    expect(features).toContain("noopener");
  });

  it("links direct to the Hardcover book page for a matched in-progress row", async () => {
    const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
    stubItems([
      item({
        title: "Mistborn",
        authors: "Brandon Sanderson",
        externalId: "B002GYI9C4",
        hardcoverBookId: 55555,
        status: "reading",
        progressPercent: 50,
      }),
    ]);
    renderWithProviders(<NowReadingTile />);

    const row = await screen.findByTestId("now-reading-row");
    await userEvent.click(row);

    expect(openSpy).toHaveBeenCalledTimes(1);
    expect(openSpy.mock.calls[0][0]).toBe("https://hardcover.app/books/55555");
  });
});
