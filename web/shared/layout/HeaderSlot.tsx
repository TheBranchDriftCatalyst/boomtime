// HeaderSlot — a domain-free "portal in state" bridge that lets a page render
// arbitrary chrome (a tab strip, a breadcrumb, actions) up into the app's
// top HeaderBar to reclaim vertical space.
//
// DOMAIN-FREE by design: pure React, zero boomtime imports, no router coupling.
// It mirrors the isolation discipline of `@shared/lib/grid` so it can graduate to
// catalyst-ui unchanged. The mechanism is a single shared ReactNode held in
// context: the header reads it; a page writes it on mount and clears it on
// unmount.
//
//   <HeaderSlotProvider>          // wrap once, around header + routed content
//     <HeaderBar/>                //   reads useHeaderSlotNode()
//     <Outlet/>                   //   a page calls useHeaderSlot(node)
//   </HeaderSlotProvider>
//
// Only ONE node lives at a time — the last mounted page wins. Because live
// routes swap the routed subtree on navigation (old page unmounts → clears,
// new page mounts → sets), a single slot is exactly right for "the current
// page's header content".
//
// ── READ/WRITE SPLIT (TALOS-6y60) ────────────────────────────────────────────
// The node and the setter live in SEPARATE contexts, and that separation is
// load-bearing — it is what makes an unstable `node` merely wasteful instead of
// fatal. With a single context holding `{node, setNode}`, the value's identity
// changed whenever the node did, so a WRITER (which must useContext to reach
// the setter) re-rendered every time it wrote. Feed such a writer a node
// derived fresh each render and that closes a cycle:
//
//   fresh node identity → effect re-runs → setNode → context value changes
//   → writer re-renders → fresh node identity → …
//
// React caps that with "Maximum update depth exceeded" and BAILS ON THE
// SUBTREE, so the routed <Outlet/> renders EMPTY while the header node stays
// painted (it is what got registered). It reads as "chrome fine, content
// blank", which misdirects debugging toward routing or lazy imports — and no
// error boundary fires, because the update-depth bail is a console.error, not a
// throw. It blanked every /app/admin/* route in production once already.
//
// Split apart, the setter context's value is the useState setter itself, which
// is stable for the provider's entire life. A writer therefore never re-renders
// as a result of its own write, the cycle has no edge back into the writer, and
// the worst an unstable node can now do is re-render the HeaderBar once per
// render of the page that owns the slot.
import { createContext, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";

type SetHeaderSlotNode = (node: ReactNode) => void;

// Two contexts, deliberately (see the READ/WRITE SPLIT note above). Never merge
// these back into one object-valued context.
const HeaderSlotNodeContext = createContext<ReactNode>(null);
const HeaderSlotSetterContext = createContext<SetHeaderSlotNode | null>(null);

/** Holds the current header node in state and exposes read/write to descendants.
 * Wrap the shell so BOTH the header and the routed content are inside it. */
export function HeaderSlotProvider({ children }: { children: ReactNode }) {
  const [node, setNode] = useState<ReactNode>(null);
  // `setNode` is a useState setter — React guarantees a stable identity for the
  // life of the provider, so the setter context needs no memoization and its
  // consumers (every useHeaderSlot caller) never re-render on a node change.
  return (
    <HeaderSlotSetterContext.Provider value={setNode}>
      <HeaderSlotNodeContext.Provider value={node}>
        {children}
      </HeaderSlotNodeContext.Provider>
    </HeaderSlotSetterContext.Provider>
  );
}

/**
 * Render `node` into the header for as long as the calling component is mounted;
 * clears it on unmount.
 *
 * `node` is the effect's dependency, so a node rebuilt every render re-runs the
 * effect every render and re-renders the HeaderBar with it. That is pure waste,
 * not a hang — the read/write split above means the write can no longer bounce
 * back into this component (see TALOS-6y60). Memoizing is still worth doing:
 * `useMemo` keyed on whatever actually changes (the active tab, say) and the
 * header updates only then.
 *
 * No-ops safely when rendered outside a HeaderSlotProvider.
 */
export function useHeaderSlot(node: ReactNode): void {
  const setNode = useContext(HeaderSlotSetterContext);
  useEffect(() => {
    if (!setNode) return;
    setNode(node);
    return () => setNode(null);
  }, [node, setNode]);
}

/** Read the current header node (null when no page has set one). The header
 * calls this to decide between page-provided chrome and its default content.
 * Subscribing to this context is what makes a component re-render on a header
 * change — do NOT call it in a component that also calls useHeaderSlot. */
export function useHeaderSlotNode(): ReactNode {
  return useContext(HeaderSlotNodeContext);
}
