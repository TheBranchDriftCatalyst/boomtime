// BookAnnotationsPanel.test.tsx — the feature-off probe and the provenance
// contract of the annotations surface (boom-siwi.5).
//
// The SPA-fallback test mirrors LiberationPanel's for the same reason it exists
// there: the Go catch-all answers an unregistered extensionless /api GET with
// HTTP 200 and the SPA index, so a truthiness check reads a flag-OFF server as
// "the feature is on". doRequest rejects HTML now, but this panel keeps its own
// independent shape guard, and this file is what pins that guard HERE rather
// than to whatever the shared client happens to do today.
import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import type { QueryClient } from "@tanstack/react-query";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import { api } from "@shared/lib/api";
import { BookAnnotationsPanel } from "./BookAnnotationsPanel";

const PATH = "/api/v1/books/items/:asin/annotations";
const ASIN = "B08X4WWQCN";

function spaFallback() {
  return new HttpResponse(
    '<!doctype html><html><head><title>boomtime</title></head><body><div id="root"></div></body></html>',
    { status: 200, headers: { "content-type": "text/html; charset=utf-8" } },
  );
}

// Waiting on the query's own state is what makes "nothing rendered" mean
// something. Without it the assertion passes trivially at t=0, before the fetch
// has resolved — green against an unguarded component.
async function probeSettled(qc: QueryClient) {
  await waitFor(() => {
    const got = qc.getQueryState(["book-annotations", ASIN])?.status;
    expect(got === "success" || got === "error").toBe(true);
  });
}

function payload(over: Record<string, unknown> = {}) {
  return {
    annotations: [
      {
        kind: "highlight",
        source: "kindle",
        positionUnit: "location",
        positionStart: 1234,
        body: "the highlighted passage",
        note: "a margin note",
      },
    ],
    counts: { highlight: 1 },
    ...over,
  };
}

describe("BookAnnotationsPanel — the feature-off probe", () => {
  it("renders nothing when the route answers with the SPA index", async () => {
    server.use(http.get(PATH, () => spaFallback()));

    const { queryClient } = renderWithProviders(<BookAnnotationsPanel asin={ASIN} />);
    await probeSettled(queryClient);

    expect(screen.queryByText(/annotation/i)).not.toBeInTheDocument();
  });

  // The shape guard pinned to THIS file: hand the hook a raw HTML string as if
  // the client had passed it through verbatim. `as never` because the point is
  // that the runtime value violates the declared return type.
  it("renders nothing when the client resolves a non-payload value", async () => {
    vi.spyOn(api, "getBookAnnotations").mockResolvedValue(
      "<!doctype html><html></html>" as never,
    );

    const { queryClient } = renderWithProviders(<BookAnnotationsPanel asin={ASIN} />);
    await probeSettled(queryClient);

    expect(screen.queryByText(/annotation/i)).not.toBeInTheDocument();
  });

  it("renders nothing for a book with no annotations", async () => {
    server.use(
      http.get(PATH, () => HttpResponse.json({ annotations: [], counts: {} })),
    );

    const { queryClient } = renderWithProviders(<BookAnnotationsPanel asin={ASIN} />);
    await probeSettled(queryClient);

    expect(screen.queryByText(/annotation/i)).not.toBeInTheDocument();
  });
});

describe("BookAnnotationsPanel — rendering", () => {
  it("shows the highlight, its note, and a position in the unit the row declares", async () => {
    server.use(http.get(PATH, () => HttpResponse.json(payload())));

    renderWithProviders(<BookAnnotationsPanel asin={ASIN} />);

    expect(await screen.findByText("the highlighted passage")).toBeInTheDocument();
    expect(screen.getByText("a margin note")).toBeInTheDocument();
    // Kindle reflowable → a LOCATION, not a page and not a timestamp.
    expect(screen.getByText("Location 1,234")).toBeInTheDocument();
  });

  it("renders a print-replica highlight as a page, not a location", async () => {
    server.use(
      http.get(PATH, () =>
        HttpResponse.json(
          payload({
            annotations: [
              {
                kind: "highlight",
                source: "kindle",
                positionUnit: "page",
                positionStart: 42,
                body: "print replica text",
              },
            ],
          }),
        ),
      ),
    );

    renderWithProviders(<BookAnnotationsPanel asin={ASIN} />);
    expect(await screen.findByText("Page 42")).toBeInTheDocument();
    expect(screen.queryByText(/Location/)).not.toBeInTheDocument();
  });

  // An Audible clip is a millisecond RANGE and must read as a timestamp span.
  // Rendering 5000 as "Location 5,000" would be actively misleading.
  it("renders an audible clip as a timestamp range", async () => {
    server.use(
      http.get(PATH, () =>
        HttpResponse.json(
          payload({
            annotations: [
              {
                kind: "clip",
                source: "audible",
                positionUnit: "millis",
                positionStart: 65_000,
                positionEnd: 92_000,
                transcript: "what the narrator said",
                transcriptSource: "whisper:large-v3",
              },
            ],
            counts: { clip: 1 },
          }),
        ),
      ),
    );

    renderWithProviders(<BookAnnotationsPanel asin={ASIN} />);
    expect(await screen.findByText("1:05–1:32")).toBeInTheDocument();
  });

  // THE PROVENANCE CONTRACT. A transcript is a speech model's guess; showing it
  // as though the user had highlighted those words would be the most misleading
  // thing this panel could do.
  it("labels a transcript with the model that produced it", async () => {
    server.use(
      http.get(PATH, () =>
        HttpResponse.json(
          payload({
            annotations: [
              {
                kind: "clip",
                source: "audible",
                positionUnit: "millis",
                positionStart: 1000,
                positionEnd: 4000,
                transcript: "what the narrator said",
                transcriptSource: "whisper:large-v3",
              },
            ],
            counts: { clip: 1 },
          }),
        ),
      ),
    );

    renderWithProviders(<BookAnnotationsPanel asin={ASIN} />);
    expect(await screen.findByText("what the narrator said")).toBeInTheDocument();
    expect(
      screen.getByText(/transcribed by whisper:large-v3/),
    ).toBeInTheDocument();
  });
});
