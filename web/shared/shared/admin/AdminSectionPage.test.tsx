// AdminSectionPage — guards for the section shell's three contracts:
// read the registry once, render the rail (including external entries), and
// drive the title row from the ACTIVE tab's registry metadata.
//
// The read-once invariant has history: getAdminGroups() derives a FRESH array
// (and fresh group objects) on every call, so reading it raw during render made
// every downstream memo miss. Back when the tab strip was hoisted into the app
// HeaderBar, that fed useHeaderSlot's identity-keyed effect a new node every
// render, its setNode bounced provider state back into this component, and the
// resulting loop tripped React's update-depth limit and blanked the routed
// <Outlet/> in production. The rail no longer goes through a slot, but the
// invariant is still the one keeping this render path cheap — and it is
// asserted via call count rather than by rendering the loop, because the loop
// blocks the event loop synchronously and a render-based assertion HANGS
// instead of failing.
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { describe, expect, it, vi } from "vitest";

// vi.mock (not vi.spyOn): AdminSectionPage holds a direct ESM import binding,
// which a spy on the module namespace never intercepts.
vi.mock("@shared/shared/admin/registry", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@shared/shared/admin/registry")>();
  return { ...actual, getAdminGroups: vi.fn(actual.getAdminGroups) };
});

import { getAdminGroups } from "@shared/shared/admin/registry";
import type { AdminGroup } from "@shared/shared/admin/types";

import { AdminSectionPage } from "./AdminSectionPage";

const GROUPS: AdminGroup[] = [
  {
    id: "core",
    label: "Operations",
    order: 0,
    tabs: [
      {
        id: "users",
        label: "Users",
        to: "/app/admin/users",
        group: "core",
        order: 0,
        width: "wide",
        description: "Roles, capability grants, and per-user overrides.",
      },
      {
        id: "apidocs",
        label: "API Docs",
        to: "/api/docs/",
        group: "core",
        order: 1,
        external: true,
      },
    ],
  },
];

function renderAt(pathname: string) {
  return render(
    <MemoryRouter initialEntries={[pathname]}>
      <Routes>
        <Route path="/app/admin" element={<AdminSectionPage />}>
          <Route path="users" element={<div>users body</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("AdminSectionPage", () => {
  it("reads the admin registry once per mount, not once per render", () => {
    vi.mocked(getAdminGroups).mockReturnValue(GROUPS);
    vi.mocked(getAdminGroups).mockClear();

    const { rerender } = renderAt("/app/admin/users");
    rerender(
      <MemoryRouter initialEntries={["/app/admin/users"]}>
        <Routes>
          <Route path="/app/admin" element={<AdminSectionPage />}>
            <Route path="users" element={<div>users body</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    // Two renders of the component, one registry read per mount. Reading it raw
    // during render returned a new array each time, which is what defeated the
    // memos downstream.
    expect(vi.mocked(getAdminGroups)).toHaveBeenCalledTimes(1);
  });

  it("titles the page from the ACTIVE tab's registry metadata", () => {
    vi.mocked(getAdminGroups).mockReturnValue(GROUPS);
    renderAt("/app/admin/users");

    // Title + description come from the registry, not from the tab body — that
    // is what stops each tab hand-rolling its own header treatment.
    expect(
      screen.getByRole("heading", { name: "Users", level: 1 }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Roles, capability grants, and per-user overrides."),
    ).toBeInTheDocument();
    // And the body the router mounted is still rendered beneath it.
    expect(screen.getByText("users body")).toBeInTheDocument();
  });

  it("falls back to the section name on a path no tab claims", () => {
    vi.mocked(getAdminGroups).mockReturnValue(GROUPS);
    renderAt("/app/admin");

    expect(
      screen.getByRole("heading", { name: "Admin", level: 1 }),
    ).toBeInTheDocument();
  });

  it("renders an external entry as a new-tab anchor, not a router link", () => {
    vi.mocked(getAdminGroups).mockReturnValue(GROUPS);
    renderAt("/app/admin/users");

    // href verbatim (NOT rewritten to a basename-relative SPA path) + a new
    // tab, so /api/docs/ reaches the Go server instead of the router's 404.
    const docs = screen.getByRole("link", { name: /API Docs/ });
    expect(docs).toHaveAttribute("href", "/api/docs/");
    expect(docs).toHaveAttribute("target", "_blank");
    expect(docs).toHaveAttribute("rel", expect.stringContaining("noreferrer"));

    // Sibling non-external entries keep routing through the NavLink path.
    expect(screen.getByRole("link", { name: "Users" })).toHaveAttribute(
      "href",
      "/app/admin/users",
    );
  });
});
