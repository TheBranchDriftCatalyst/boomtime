import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "@/test/msw/server";
import { renderWithProviders } from "@/test/renderWithProviders";
import {
  WIDGET_CATALOG,
  catalogFor,
  embedSnippets,
  widgetSvgUrl,
} from "./catalog";
import { WidgetCard } from "./WidgetCard";
import { WidgetsPanel } from "./WidgetsPanel";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

const BASE = "http://localhost:8080/widget/svg/abc-123";

describe("catalog", () => {
  it("filters entries by scope", () => {
    const user = catalogFor("user").map((e) => e.kind);
    expect(user).toContain("stats-card-with-grade");
    expect(user).toContain("top-projects");

    const project = catalogFor("project").map((e) => e.kind);
    // grade is person-tuned and top-projects is meaningless inside one project
    expect(project).not.toContain("stats-card-with-grade");
    expect(project).not.toContain("top-projects");
    expect(project).toContain("top-langs");
  });

  it("every entry carries primitives metadata for the v2 builder", () => {
    for (const e of WIDGET_CATALOG) {
      expect(e.primitives.length).toBeGreaterThan(0);
    }
  });

  it("builds widget URLs with range + theme params", () => {
    expect(widgetSvgUrl(BASE, "top-langs", { days: 30, theme: "dark" })).toBe(
      `${BASE}/top-langs?days=30&theme=dark`,
    );
  });

  it("builds the three embed snippet formats", () => {
    const s = embedSnippets("https://x/widget/svg/u/stats-card?days=30&theme=dark");
    expect(s.markdown).toBe(
      "![Coding stats](https://x/widget/svg/u/stats-card?days=30&theme=dark)",
    );
    expect(s.html).toContain('<img src="https://x/widget/svg/u/stats-card');
    expect(s.url).toContain("/widget/svg/u/stats-card");
  });
});

describe("WidgetCard", () => {
  it("copies the Markdown snippet on click", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    renderWithProviders(
      <WidgetCard
        entry={WIDGET_CATALOG[0]}
        baseUrl={BASE}
        days={30}
        theme="dark"
      />,
    );

    await userEvent.click(
      screen.getByRole("button", { name: /Copy Markdown/i }),
    );
    expect(writeText).toHaveBeenCalledWith(
      `![Coding stats](${BASE}/stats-card?days=30&theme=dark)`,
    );
  });

  it("renders a live preview object pointing at the public URL", () => {
    renderWithProviders(
      <WidgetCard
        entry={WIDGET_CATALOG[2] /* top-langs */}
        baseUrl={BASE}
        days={7}
        theme="light"
      />,
    );
    const preview = screen.getByLabelText(/Top Languages preview/i);
    expect(preview).toHaveAttribute(
      "data",
      `${BASE}/top-langs?days=7&theme=light`,
    );
  });
});

describe("WidgetsPanel", () => {
  it("mints the link on open and shows the scope's catalog", async () => {
    let minted = 0;
    server.use(
      http.get("/api/v1/users/current/widgets/link", ({ request }) => {
        minted++;
        const url = new URL(request.url);
        expect(url.searchParams.get("scopeType")).toBe("user");
        return HttpResponse.json({
          widgetBaseUrl: BASE,
          linkId: "abc-123",
        });
      }),
    );

    renderWithProviders(<WidgetsPanel scopeType="user" />);
    expect(minted).toBe(0); // lazy: no mint before open

    await userEvent.click(
      screen.getByRole("button", { name: /Open widgets panel/i }),
    );
    await waitFor(() => expect(minted).toBe(1));

    // user scope shows the full catalog incl. the grade card
    for (const entry of catalogFor("user")) {
      expect(await screen.findByText(entry.title)).toBeInTheDocument();
    }
  });

  it("project scope hides user-only widgets", async () => {
    server.use(
      http.get("/api/v1/users/current/widgets/link", () =>
        HttpResponse.json({ widgetBaseUrl: BASE, linkId: "abc-123" }),
      ),
    );
    renderWithProviders(<WidgetsPanel scopeType="project" scopeRef="proj-x" />);
    await userEvent.click(
      screen.getByRole("button", { name: /Open widgets panel/i }),
    );
    expect(await screen.findByText("Top Languages")).toBeInTheDocument();
    expect(screen.queryByText("Stats Card + Grade")).not.toBeInTheDocument();
    expect(screen.queryByText("Top Projects")).not.toBeInTheDocument();
  });
});
