// LiberationPanel.test.tsx — the availability probe and the mutation-feedback
// contract of the liberation surface (boom-l827).
//
// TWO LAYERS ON PURPOSE.
//
//  1. Integration (msw + the REAL @shared/lib/api client): what a liberation-off
//     boomtime actually puts on the wire — 200 text/html from the Go SPA
//     catch-all — must produce no liberation UI. This is the end-to-end
//     contract and it spans both the api client and this hook.
//
//  2. Hook-level (api.getLiberationStatus stubbed): the same assertion with the
//     api client taken OUT of the picture, by handing the hook a raw HTML string
//     as if the client had returned it verbatim. Layer 1 alone would silently
//     stop testing this component the moment the shared client starts rejecting
//     non-JSON — the availability decision would be made entirely upstream and a
//     regression here would go unnoticed. Layer 2 pins the guard to THIS file.
import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { QueryClient } from "@tanstack/react-query";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import { api } from "@shared/lib/api";
import { LiberateAllButton, LiberationPanel } from "./LiberationPanel";

const STATUS_PATH = "/api/v1/books/liberation/status";
const EXCLUDED_PATH = "/api/v1/books/liberation/excluded";
const ITEM_PATH = "/api/v1/books/items/:id/liberate";

/**
 * What the Go SPA catch-all actually serves for an UNREGISTERED /api GET on
 * this server: 200 text/html with the SPA index. Not a 404 — the catch-all only
 * 404s paths whose last segment contains a dot, and this one is extensionless.
 */
function spaFallback() {
  return new HttpResponse(
    "<!doctype html><html><head><title>boomtime</title></head><body><div id=\"root\"></div></body></html>",
    { status: 200, headers: { "content-type": "text/html; charset=utf-8" } },
  );
}

/**
 * Block until the availability probe has actually SETTLED.
 *
 * Without this every "nothing rendered" assertion below would pass trivially at
 * t=0 — before the fetch resolves there is nothing on screen either way, so the
 * test would go green against the unfixed hook. Waiting on the query's own
 * state makes the assertion mean "the probe came back and we still render
 * nothing", which is the actual contract.
 */
async function probeSettled(qc: QueryClient, want?: "success" | "error") {
  await waitFor(() => {
    const got = qc.getQueryState(["liberation", "status"])?.status;
    expect(want ? got === want : got === "success" || got === "error").toBe(true);
  });
}

/**
 * Stub the api client so it hands the hook exactly what a client with a
 * text-fallback would: the raw body, unparsed and untyped. `as never` because
 * the whole point is that the RUNTIME value violates the declared return type —
 * which is precisely the situation the shape guard exists for.
 */
function stubStatusResolving(value: unknown) {
  return vi
    .spyOn(api, "getLiberationStatus")
    .mockResolvedValue(value as never);
}

function statusPayload(over: Record<string, unknown> = {}) {
  return {
    counts: { liberated: 1, pending: 2 },
    pending: 2,
    excluded: 0,
    libraryPath: "/mnt/library",
    ...over,
  };
}

describe("useLiberationAvailable — the feature-off probe", () => {
  it("renders NO liberation surface when the status route answers with the SPA index", async () => {
    server.use(http.get(STATUS_PATH, () => spaFallback()));

    const { queryClient } = renderWithProviders(
      <>
        <LiberationPanel externalId="B08GB58KD5" source="audible" />
        <LiberateAllButton />
      </>,
    );

    // Deliberately NOT pinned to success-vs-error: the api client is free to
    // reject the non-JSON body itself. What is fixed is the OUTCOME on screen.
    await probeSettled(queryClient);

    expect(screen.queryByText("Liberation")).not.toBeInTheDocument();
    // The per-book control must not appear at all — a visible Liberate button
    // POSTs to an unregistered route and comes back 405 from the catch-all.
    expect(
      screen.queryByRole("button", { name: /Liberate/ }),
    ).not.toBeInTheDocument();
    // …and the toolbar must not claim "Every book is already liberated", which
    // is what `(htmlString).pending ?? 0` produced.
    expect(
      screen.queryByTitle("Every book is already liberated"),
    ).not.toBeInTheDocument();
  });

  it("renders NO liberation surface when the probe fails outright (404 / network)", async () => {
    server.use(
      http.get(STATUS_PATH, () =>
        HttpResponse.json({ message: "not found" }, { status: 404 }),
      ),
    );

    const { queryClient } = renderWithProviders(
      <>
        <LiberationPanel externalId="B08GB58KD5" source="audible" />
        <LiberateAllButton />
      </>,
    );

    await probeSettled(queryClient, "error");

    expect(screen.queryByText("Liberation")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Liberate/ }),
    ).not.toBeInTheDocument();
  });

  // The paired positive case: the shape guard must not be so strict that a
  // genuine liberation-ON install loses its UI. Without this, "return false
  // always" would satisfy the two tests above.
  it("renders the full surface when the status route answers with a real status payload", async () => {
    server.use(http.get(STATUS_PATH, () => HttpResponse.json(statusPayload())));

    renderWithProviders(
      <>
        <LiberationPanel externalId="B08GB58KD5" source="audible" />
        <LiberateAllButton />
      </>,
    );

    expect(await screen.findByText("Liberation")).toBeInTheDocument();
    // The toolbar reads its count off the payload, not off `undefined ?? 0`.
    expect(
      await screen.findByRole("button", { name: "Liberate all (2)" }),
    ).toBeInTheDocument();
  });

  // An empty Audible library still returns a valid status — `counts` is `{}`.
  // The guard keys on the PRESENCE of counts, so this must stay available.
  it("stays available when counts is an empty object", async () => {
    server.use(
      http.get(STATUS_PATH, () =>
        HttpResponse.json(statusPayload({ counts: {}, pending: 0 })),
      ),
    );

    renderWithProviders(<LiberationPanel externalId="B1" source="audible" />);

    expect(await screen.findByText("Liberation")).toBeInTheDocument();
  });

  // --- Layer 2: the guard itself, with the api client taken out of the loop ---
  //
  // These stub api.getLiberationStatus into RESOLVING with a non-status value,
  // reproducing a client that hands back whatever the body was. If the hook goes
  // back to `available: !q.isError && !!q.data` these fail; the msw tests above
  // would not, because the shared client rejects the body before the hook sees it.
  it.each([
    [
      "the SPA index page as a raw string",
      "<!doctype html><html><body><div id=\"root\"></div></body></html>",
    ],
    ["a JSON error envelope", { message: "route not found" }],
    ["an empty string", ""],
    ["an array", []],
    ["a status missing `pending`", { counts: {}, libraryPath: "/mnt" }],
  ])("treats %s as feature-off", async (_label, body) => {
    stubStatusResolving(body);

    const { queryClient } = renderWithProviders(
      <>
        <LiberationPanel externalId="B1" source="audible" />
        <LiberateAllButton />
      </>,
    );

    await probeSettled(queryClient);

    expect(screen.queryByText("Liberation")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Liberate/ }),
    ).not.toBeInTheDocument();
  });

  it("stays available when the api client hands back a real status object", async () => {
    stubStatusResolving(statusPayload());

    renderWithProviders(<LiberateAllButton />);

    expect(
      await screen.findByRole("button", { name: "Liberate all (2)" }),
    ).toBeInTheDocument();
  });

  it("stays hidden for a Kindle title even when the feature is on", async () => {
    server.use(http.get(STATUS_PATH, () => HttpResponse.json(statusPayload())));

    const { queryClient } = renderWithProviders(
      <LiberationPanel externalId="B1" source="kindle" />,
    );

    await probeSettled(queryClient, "success");

    expect(screen.queryByText("Liberation")).not.toBeInTheDocument();
  });
});

describe("liberation mutation feedback", () => {
  it("shows a failed Retry in the skipped list instead of silently dropping it", async () => {
    server.use(
      http.get(STATUS_PATH, () =>
        HttpResponse.json(statusPayload({ excluded: 1 })),
      ),
      http.get(EXCLUDED_PATH, () =>
        HttpResponse.json({
          items: [
            {
              asin: "B0RETRY",
              title: "A Title Amazon Keeps Refusing",
              status: "failed",
              error: "license request failed",
              attempts: 3,
              retryable: true,
            },
          ],
        }),
      ),
      // The retry (DELETE .../liberate) fails — a 500, a network drop, a
      // transient DB error. Before the fix this produced NOTHING on screen.
      http.delete(ITEM_PATH, () =>
        HttpResponse.json({ message: "database is on fire" }, { status: 500 }),
      ),
    );

    renderWithProviders(<LiberateAllButton />);

    await userEvent.click(await screen.findByRole("button", { name: /1 skipped/ }));
    await userEvent.click(await screen.findByRole("button", { name: /Retry/ }));

    // The row is still there (the retry did not take), so the ONLY thing that
    // can tell the user apart from success is this message.
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Retry failed: database is on fire");
    expect(alert).toHaveClass("text-destructive");
    // The excluded row is untouched — proving the message is not a redraw.
    expect(
      screen.getByText("A Title Amazon Keeps Refusing"),
    ).toBeInTheDocument();
  });

  it("renders a failed liberate destructively, not as a muted status line", async () => {
    server.use(
      http.get(STATUS_PATH, () => HttpResponse.json(statusPayload())),
      http.post(ITEM_PATH, () =>
        HttpResponse.json({ message: "liberation is not enabled" }, { status: 500 }),
      ),
    );

    renderWithProviders(<LiberationPanel externalId="B1" source="audible" />);

    await userEvent.click(await screen.findByRole("button", { name: "Liberate" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("liberation is not enabled");
    // The bug: success and failure shared `text-muted-foreground`, so a failed
    // sweep read as an informational status update.
    expect(alert).toHaveClass("text-destructive");
    expect(alert).not.toHaveClass("text-muted-foreground");
  });

  it("keeps a SUCCESSFUL liberate muted and un-alerted", async () => {
    server.use(
      http.get(STATUS_PATH, () => HttpResponse.json(statusPayload())),
      http.post(ITEM_PATH, () =>
        HttpResponse.json({ enqueued: true, jobId: 42, asin: "B1" }),
      ),
    );

    renderWithProviders(<LiberationPanel externalId="B1" source="audible" />);

    await userEvent.click(await screen.findByRole("button", { name: "Liberate" }));

    const line = await screen.findByText(/Queued \(job 42\)/);
    expect(line).toHaveClass("text-muted-foreground");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
