// CatalystBooks domain registration module.
//
// The book bridge + external-account connectors. Registers the Books nav
// destination + page route, the Connections settings tab (all provider links
// live here), and the Books admin tab + route (source-diagnostics /
// reading-monitor / raw-feed bridge). Imports only the shell register API + its
// own things; the page/tab bodies are lazy() so registration stays cheap +
// side-effect free, and a boomtime-free (books-only) build keeps working.
import { lazy, Suspense } from "react";
import { Library } from "lucide-react";

import { registerNavItem } from "@shared/shared/nav/registry";
import { registerSettingsSection } from "@shared/shared/settings/registry";
import { registerAdminTab } from "@shared/shared/admin/registry";
import { registerRoute } from "@shared/shared/routing/registry";
import { PageFallback } from "@shared/shared/routing/PageFallback";

const ConnectionsTab = lazy(() =>
  import("./ConnectionsTab").then((m) => ({ default: m.ConnectionsTab })),
);
// gaka-books: read-only library view of the siloed reading_items (Audible now,
// Kindle later). Gated in the sidebar on books_enabled; the route itself is
// always mounted (the page renders a disabled-state card when the flag is off).
const Books = lazy(() =>
  import("@books/features/books/BooksPage").then((m) => ({ default: m.BooksPage })),
);
const BooksTab = lazy(() =>
  import("@books/features/admin/BooksTab").then((m) => ({ default: m.BooksTab })),
);

export function registerBooksDomain(): void {
  // ── Nav ────────────────────────────────────────────────────────────────
  // Books is its own top-level destination today, gated on books_enabled so
  // it's fully inert on deployments that don't run the feature.
  registerNavItem(
    { id: "books", order: 10 },
    { name: "Books", icon: Library, to: "/app/books", flag: "books_enabled" },
  );

  // ── Routes ─────────────────────────────────────────────────────────────
  // The Books library page (/app/books) and the Books admin tab
  // (/app/admin/books) hang off the core-owned "app" / "admin" shell mounts.
  registerRoute({
    parent: "app",
    path: "books",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Books />
      </Suspense>
    ),
    order: 40,
  });
  registerRoute({
    parent: "admin",
    path: "books",
    element: (
      <Suspense fallback={<PageFallback />}>
        <BooksTab />
      </Suspense>
    ),
    order: 70,
  });

  // ── Settings (CatalystBooks group) ─────────────────────────────────────
  registerSettingsSection({
    id: "catalystbooks",
    label: "CatalystBooks",
    order: 10,
    tabs: [
      { id: "connections", label: "Connections", order: 0, render: () => <ConnectionsTab /> },
    ],
  });

  // ── Admin (CatalystBooks group) ────────────────────────────────────────
  registerAdminTab({
    id: "books",
    label: "Books",
    to: "/app/admin/books",
    group: { id: "catalystbooks", label: "CatalystBooks", order: 10 },
    order: 0,
  });
}
