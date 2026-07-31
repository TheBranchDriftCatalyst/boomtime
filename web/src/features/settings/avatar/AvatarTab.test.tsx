// AvatarTab.test.tsx (gaka-9v4) — smoke + integration for the
// PROFILE SYNTHESIS console. Non-tautological: mocks the LLM SSE endpoint
// with a hand-crafted stream response and asserts that
//   (a) the RENDER button is DISABLED until the textarea has content, and
//   (b) it BECOMES enabled after the SSE stream lands one delta.
// This catches the exact regression that would ship a "click RENDER
// with an empty prompt" 400.
import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AvatarTab, deriveSummary } from "@/features/settings/avatar/AvatarTab";
import { authStore } from "@/features/auth/auth";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

// Minimal `/auth/users/current` + `/avatar/status` handlers so the tab
// mounts without hitting the msw onUnhandledRequest guard.
function baseHandlers(username = "panda") {
  return [
    // Backend shape: {data: {full_name, email, photo, is_admin}}. The
    // acting username is jammed into `full_name` (see internal/handler/
    // auth.go CurrentUser) — matches what AvatarTab reads.
    http.get("/auth/users/current", () =>
      HttpResponse.json({
        data: {
          full_name: username,
          email: `${username}@x.dev`,
          photo: "",
          is_admin: true,
        },
      }),
    ),
    http.get("/api/v1/users/current/avatar/status", () =>
      HttpResponse.json({ status: "none" }),
    ),
  ];
}

beforeEach(() => {
  authStore.update({
    token: "test-token",
    tokenExpiry: new Date(Date.now() + 60_000).toISOString(),
    tokenUsername: "panda",
  });
  server.use(...baseHandlers());
});

// deriveSummary: percentage scale regression tests. The call site
// passes ResourceStats.totalPct (0..1 decimal from the backend rollup
// SQL — see WidgetRenderer's ChipList note) as `pct`, so the label
// path MUST multiply by 100. A regression that drops the *100 would
// render "PYTHON 0%" for a 15%-of-30d language.
describe("deriveSummary — top-language percent scale", () => {
  it("renders topLanguage.pct=0.15 as '15%' (not '0%')", () => {
    const s = deriveSummary({
      topLanguage: { name: "python", pct: 0.15 },
    });
    expect(s.topLabels[0]).toBe("PYTHON 15%");
  });

  it("renders topLanguage.pct=1.0 as '100%' (full-share language)", () => {
    // A single-language 30d window is the edge case where the raw 0..1
    // value looks identical to a percent-scaled '1%' at first glance —
    // pin it so the regression can't sneak past.
    const s = deriveSummary({
      topLanguage: { name: "go", pct: 1.0 },
    });
    expect(s.topLabels[0]).toBe("GO 100%");
  });

  it("falls back to 'NEW OPERATOR' when there's no dominant trait at all", () => {
    // Preserved behavior — otherwise the LLM synthesizer chokes on an
    // empty topLabels array.
    const s = deriveSummary({});
    expect(s.topLabels).toEqual(["NEW OPERATOR"]);
  });
});

describe("AvatarTab", () => {
  it("mounts and renders the three panel headers", async () => {
    renderWithProviders(<AvatarTab />);
    // Panel titles are the "PROFILE SYNTHESIS · BIOMETRIC RENDER" header
    // + three column subheaders. Assert on all three column headers so a
    // future column-drop regression fails loudly.
    expect(
      await screen.findByText(/PROFILE SYNTHESIS · BIOMETRIC RENDER/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/INPUT CONTEXT/i)).toBeInTheDocument();
    expect(screen.getByText(/PROMPT SYNTHESIS/i)).toBeInTheDocument();
    expect(screen.getByText(/OUTPUT \/ BIOMETRIC/i)).toBeInTheDocument();
  });

  it("keeps the RENDER button disabled while the prompt textarea is empty", async () => {
    renderWithProviders(<AvatarTab />);
    const renderBtn = await screen.findByTestId("avatar-render-btn");
    expect(renderBtn).toBeDisabled();
  });

  it("enables the RENDER button once the textarea has content", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AvatarTab />);
    const textarea = (await screen.findByTestId(
      "avatar-prompt-textarea",
    )) as HTMLTextAreaElement;
    await user.type(textarea, "chibi portrait, cel shading");
    const renderBtn = screen.getByTestId("avatar-render-btn");
    await waitFor(() => expect(renderBtn).not.toBeDisabled());
  });

  it("streams the SSE prompt into the textarea when SYNTHESIZE is clicked", async () => {
    // gaka-say: the previous MSW-proxied version tripped on a jsdom-vs-undici
    // AbortController mismatch — MSW's `instanceof AbortSignal` check fires
    // against jsdom's shim, not Node's native class, so the fetch immediately
    // rejected with "Expected signal ("AbortSignal {}") to be an instance of
    // AbortSignal" before the stream ever ran. Stubbing fetch directly
    // sidesteps that entire class hierarchy — same observable assertion, no
    // MSW-in-the-middle. If we ever swap to happy-dom or MSW ships a fix, we
    // can restore the MSW handler form.
    const encoder = new TextEncoder();
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(
          encoder.encode(
            'data: {"choices":[{"delta":{"content":"chibi portrait"}}]}\n',
          ),
        );
        controller.enqueue(
          encoder.encode(
            'data: {"choices":[{"delta":{"content":", cel shading"}}]}\n',
          ),
        );
        controller.enqueue(encoder.encode("data: [DONE]\n"));
        controller.close();
      },
    });
    const originalFetch = globalThis.fetch;
    const fetchStub = vi
      .fn<typeof fetch>()
      .mockImplementation(
        async (input: RequestInfo | URL, _init?: RequestInit) => {
          const url = typeof input === "string" ? input : input.toString();
          if (url.includes("/api/v1/admin/avatar/synthesize-prompt")) {
            return new Response(stream, {
              status: 200,
              headers: { "Content-Type": "text/event-stream" },
            });
          }
          // Delegate everything else (MSW handlers for auth/avatar status
          // are still needed for the tab mount).
          return originalFetch(input, _init);
        },
      );
    globalThis.fetch = fetchStub as unknown as typeof fetch;
    try {
      const user = userEvent.setup();
      renderWithProviders(<AvatarTab />);

      await user.click(await screen.findByTestId("avatar-synthesize-btn"));

      // After the stream drains, the textarea should contain the concatenated
      // deltas. Wait on the final assembled text — a plain
      // toHaveValue on the interim state would race the second frame.
      await waitFor(
        () => {
          const textarea = screen.getByTestId(
            "avatar-prompt-textarea",
          ) as HTMLTextAreaElement;
          expect(textarea.value).toBe("chibi portrait, cel shading");
        },
        { timeout: 2000 },
      );
      // And the RENDER button enables as a side effect.
      expect(screen.getByTestId("avatar-render-btn")).not.toBeDisabled();
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
