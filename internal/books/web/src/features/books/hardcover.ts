// hardcover.ts — build a Hardcover (hardcover.app) deep-link for a tracked book
// (gaka-qic0). Clicking a book in the Now-reading tile or the Books-page table
// should land as close to that book's Hardcover page as we can resolve.
//
// Resolution order, most precise first:
//   (a) a resolved Hardcover book id/slug (once the match sync has run) → the
//       DIRECT book page (id works as the path today; a slug is preferred when
//       present). This is the honest destination once hardcoverBookId is set.
//   (b) an ASIN (external_id / amazonAsin) → an ASIN-precise Hardcover SEARCH.
//       An ASIN pins a single edition far more exactly than a fuzzy title.
//   (c) title + authors search — the last-resort fallback.
import type { ReadingItemDTO } from "@shared/types/meta";

// Accept the DTO's linking fields. `hardcoverBookId` is the resolved match
// (migration 00063); `hardcoverSlug`/`hardcoverId` remain honored for when the
// backend later emits a slug. `externalId`/`amazonAsin` carry the ASIN.
type HardcoverLinkable = Pick<ReadingItemDTO, "title" | "authors"> & {
  hardcoverBookId?: number | string | null;
  externalId?: string | null;
  amazonAsin?: string | null;
  hardcoverSlug?: string | null;
  hardcoverId?: string | number | null;
};

const BASE = "https://hardcover.app";

const clean = (v: unknown): string =>
  v === undefined || v === null ? "" : String(v).trim();

export function hardcoverUrl(item: HardcoverLinkable): string {
  // (a) direct book page — a slug wins over a numeric id when both are present.
  const slug = clean(item.hardcoverSlug);
  if (slug) return `${BASE}/books/${encodeURIComponent(slug)}`;

  const id = clean(item.hardcoverBookId) || clean(item.hardcoverId);
  if (id) return `${BASE}/books/${encodeURIComponent(id)}`;

  // (b) ASIN-precise search — external_id is the ASIN; amazonAsin is the
  // print/kindle sibling. Either pins the edition better than the title.
  const asin = clean(item.amazonAsin) || clean(item.externalId);
  if (asin) return `${BASE}/search?q=${encodeURIComponent(asin)}`;

  // (c) title + authors search.
  const q = [item.title, item.authors].filter(Boolean).join(" ").trim();
  return `${BASE}/search?q=${encodeURIComponent(q)}`;
}

/** Open a book's Hardcover page in a new, isolated tab. */
export function openHardcover(item: HardcoverLinkable): void {
  window.open(hardcoverUrl(item), "_blank", "noopener,noreferrer");
}
