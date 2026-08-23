// PageActionsSlot — the seam that lets a routed tab body push DYNAMIC chrome
// into a page header it does not own (boom-9e9k).
//
// The problem it solves: once a section shell (Admin, Settings) renders each
// tab's header from the registration DSL — title, description, width — a tab
// still needs somewhere to put controls that only IT can build, because they
// close over its own state and queries: Labels' "Regen all (146)", Jobs'
// "Clear all logs", Data's export button. Without a slot those tabs must
// re-hand-roll a header row to hold them, which is exactly the divergence the
// registry-driven header exists to end (three tabs, three different header
// treatments).
//
// So: static metadata flows DOWN from the registry, dynamic nodes flow UP
// through this slot, and the shell composes both into one header. A tab body
// renders content and nothing else.
//
//   function LabelsTab() {
//     const { data } = useLabels();
//     usePageActions(
//       useMemo(
//         () => <Button onClick={regenAll}>Regen all ({data?.length ?? 0})</Button>,
//         [data?.length],
//       ),
//     );
//     return <LabelTable rows={data} />;   // no title, no max-w, no header row
//   }
//
// MEMOIZE the node (as above). It is the write effect's dependency; an inline
// element rebuilt every render re-runs the effect and re-renders the shell
// header each time. That's waste rather than a hang — see createNodeSlot's
// read/write-split note for why it can no longer loop.
import { createNodeSlot } from "@shared/layout/createNodeSlot";

const slot = createNodeSlot("PageActions");

/**
 * Wrap a section shell so its header (reader) and its routed tab bodies
 * (writers) share one action slot. Mount it INSIDE the section, not app-wide:
 * one slot holds one node, and scoping it per section means a tab's actions
 * are torn down automatically when the router swaps the tab out.
 */
export const PageActionsProvider = slot.Provider;

/** Push `node` into the enclosing section's page header while mounted. */
export const usePageActions = slot.useSet;

/** Read the current page-action node — for the section shell's header only. */
export const usePageActionsNode = slot.useNode;

/**
 * Renders whatever is currently in the slot. The section shell reads the node
 * directly (it needs to know whether there IS one before deciding to render a
 * header row at all), so this exists mainly for TESTS: a tab body that pushes
 * its controls up now renders them nowhere on its own, and a test that mounts
 * the tab in isolation would find no button to click. Wrapping the subject in
 * `<PageActionsProvider><PageActionsOutlet/>…</PageActionsProvider>` reproduces
 * the shell's composition without pulling in the whole section page.
 */
export function PageActionsOutlet() {
  return <>{usePageActionsNode()}</>;
}
