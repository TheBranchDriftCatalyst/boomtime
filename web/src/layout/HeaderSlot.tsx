// HeaderSlot — a domain-free "portal in state" bridge that lets a page render
// arbitrary chrome (a tab strip, a breadcrumb, actions) up into the app's
// top HeaderBar to reclaim vertical space.
//
// DOMAIN-FREE by design: pure React, zero boomtime imports, no router coupling.
// It mirrors the isolation discipline of `@/lib/grid` so it can graduate to
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
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import type { ReactNode } from "react";

interface HeaderSlotContextValue {
  node: ReactNode;
  setNode: (node: ReactNode) => void;
}

const HeaderSlotContext = createContext<HeaderSlotContextValue | null>(null);

/** Holds the current header node in state and exposes read/write to descendants.
 * Wrap the shell so BOTH the header and the routed content are inside it. */
export function HeaderSlotProvider({ children }: { children: ReactNode }) {
  const [node, setNode] = useState<ReactNode>(null);
  // Stable context value so consumers of the SETTER don't re-render when the
  // node itself changes (only the reader, useHeaderSlotNode, should).
  const value = useMemo<HeaderSlotContextValue>(
    () => ({ node, setNode }),
    [node],
  );
  return (
    <HeaderSlotContext.Provider value={value}>
      {children}
    </HeaderSlotContext.Provider>
  );
}

/**
 * Render `node` into the header for as long as the calling component is mounted;
 * clears it on unmount.
 *
 * IMPORTANT: pass a **memoized** `node` (e.g. via `useMemo`). It is the effect's
 * dependency, so an inline `<Tabs/>` rebuilt every render would re-run the
 * effect on every render and thrash the header. Memoize it keyed on whatever
 * actually changes (the active tab, say) and the header updates only then.
 *
 * No-ops safely when rendered outside a HeaderSlotProvider.
 */
export function useHeaderSlot(node: ReactNode): void {
  const ctx = useContext(HeaderSlotContext);
  const setNode = ctx?.setNode;
  useEffect(() => {
    if (!setNode) return;
    setNode(node);
    return () => setNode(null);
  }, [node, setNode]);
}

/** Read the current header node (null when no page has set one). The header
 * calls this to decide between page-provided chrome and its default content. */
export function useHeaderSlotNode(): ReactNode {
  const ctx = useContext(HeaderSlotContext);
  return ctx?.node ?? null;
}
