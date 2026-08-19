// CatalogPage.test.tsx — gaka-qcxg: the reading-domain widgets are books_enabled
// -gated in the gallery. This renders the PUBLIC catalog (variant="public",
// which forces the zero-network sample source) and flips the mocked
// usePublicConfig flag, asserting the Reading section + its cards appear ONLY
// when books_enabled is on. Non-tautological: it asserts the actual rendered
// card titles + category header, not the gate helper in isolation
// (catalogEntries.test.ts covers that).
import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/renderWithProviders";
import type { PublicConfig } from "@/types/meta";
import { __resetReadingRange } from "@/features/overview/reading/readingRange";

// Control the boot config the page gates on. Every other flag stays fixed;
// only books_enabled varies per test.
let booksEnabled = false;
vi.mock("@/lib/usePublicConfig", () => ({
  usePublicConfig: (): { config: PublicConfig; isLoading: boolean } => ({
    isLoading: false,
    config: {
      registration_enabled: true,
      auth_provider: "local",
      oidc_enabled: false,
      billing_enabled: false,
      beta_flags: {},
      github_connect_enabled: false,
      books_enabled: booksEnabled,
    },
  }),
}));

import { CatalogPage } from "./CatalogPage";

// The five reading catalog-card titles (WidgetCatalogEntry.title).
const READING_TITLES = [
  "Listening Trend",
  "Books by Genre",
  "Top Series by Runtime",
  "Finished per Month",
  "Listening in Range",
];

beforeEach(() => {
  __resetReadingRange();
});

describe("CatalogPage — books_enabled gate on the reading widgets", () => {
  it("LISTS the Reading section + every reading card when books_enabled is on", async () => {
    booksEnabled = true;
    renderWithProviders(<CatalogPage variant="public" />, { withRouter: true });

    // The category section header renders once the reading group is non-empty.
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: /Reading/ })).toBeInTheDocument(),
    );
    for (const title of READING_TITLES) {
      expect(screen.getByText(title), title).toBeInTheDocument();
    }
  });

  it("HIDES the Reading section + every reading card when books_enabled is off", async () => {
    booksEnabled = false;
    renderWithProviders(<CatalogPage variant="public" />, { withRouter: true });

    // A non-reading widget still renders — proves the page mounted, so the
    // absence below is a real gate, not a failed render.
    await waitFor(() =>
      expect(screen.getByText("Stats Card")).toBeInTheDocument(),
    );
    expect(screen.queryByRole("heading", { name: /Reading/ })).not.toBeInTheDocument();
    for (const title of READING_TITLES) {
      expect(screen.queryByText(title), title).not.toBeInTheDocument();
    }
  });
});
