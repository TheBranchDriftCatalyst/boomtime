// AvatarTab.test.tsx (gaka-9v4) — smoke + integration for the
// PROFILE SYNTHESIS console. Non-tautological: mocks the LLM SSE endpoint
// with a hand-crafted stream response and asserts that
//   (a) the RENDER button is DISABLED until the textarea has content, and
//   (b) it BECOMES enabled after the SSE stream lands one delta.
// This catches the exact regression that would ship a "click RENDER
// with an empty prompt" 400.
import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AvatarTab } from "@/features/settings/avatar/AvatarTab";
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
    // Encode a two-frame OpenAI-shaped SSE body. We use a hand-rolled
    // Response with a ReadableStream so msw can proxy the streaming path.
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
    server.use(
      http.post(
        "/api/v1/admin/avatar/synthesize-prompt",
        () =>
          new HttpResponse(stream, {
            status: 200,
            headers: { "Content-Type": "text/event-stream" },
          }),
      ),
    );

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
  });
});
