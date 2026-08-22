// AdminSectionPage.test.tsx — regression guard for the blank Admin content pane.
//
// AdminSectionPage hoists its tab strip into the HeaderBar via useHeaderSlot,
// whose effect is keyed on the node's IDENTITY. getAdminGroups() derives a
// FRESH array (and fresh group objects) on every call, so reading it raw during
// render made the useMemo below it miss every time: the effect re-ran, its
// setNode bounced HeaderSlotProvider's state back into this component, and the
// resulting render loop tripped React's update-depth limit and blanked the
// routed <Outlet/>. The tab strip still painted (it IS the header node), which
// is why the bug read as "chrome fine, content empty".
//
// The invariant that fixes it: the registry is read ONCE per mount, so the
// memoized header node keeps a stable identity across re-renders. Asserted via
// call count rather than by rendering the loop — the loop blocks the event loop
// synchronously, so a render-based assertion HANGS instead of failing.
import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

// vi.mock (not vi.spyOn): AdminSectionPage holds a direct ESM import binding,
// which a spy on the module namespace never intercepts.
vi.mock("@shared/shared/admin/registry", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@shared/shared/admin/registry")>();
  return { ...actual, getAdminGroups: vi.fn(actual.getAdminGroups) };
});

import { getAdminGroups } from "@shared/shared/admin/registry";

// Rendered WITHOUT a HeaderSlotProvider: useHeaderSlot no-ops outside one, so
// the feedback loop can't form and this test stays deterministic either way.
import { AdminSectionPage } from "./AdminSectionPage";

describe("AdminSectionPage", () => {
  it("reads the admin registry once per mount, not once per render", () => {
    vi.mocked(getAdminGroups).mockClear();

    const { rerender } = render(<AdminSectionPage />);
    rerender(<AdminSectionPage />);
    rerender(<AdminSectionPage />);

    // Three renders, one registry read. Reading it raw during render returned a
    // new array each time, which is what defeated the header-slot memo.
    expect(vi.mocked(getAdminGroups)).toHaveBeenCalledTimes(1);
  });
});
