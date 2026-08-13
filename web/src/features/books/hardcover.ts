// hardcover.ts — build a Hardcover (hardcover.app) deep-link for a tracked book
// (gaka-qic0). Clicking a book in the Now-reading tile or the Books-page table
// should land on that book's Hardcover page.
//
// The reading-items DTO does NOT (yet) carry a Hardcover slug/id, so today every
// link falls back to a Hardcover SEARCH by "<title> <authors>". The helper is
// written to prefer a direct book page the moment the DTO grows a
// `hardcoverSlug` / `hardcoverId` — no call-site change needed then.
import type { ReadingItemDTO } from "@/types/meta";

// Accept the DTO plus the not-yet-present identifier fields, so the direct-link
// branch is live the day the backend starts emitting them.
type HardcoverLinkable = Pick<ReadingItemDTO, "title" | "authors"> & {
  hardcoverSlug?: string | null;
  hardcoverId?: string | number | null;
};

const BASE = "https://hardcover.app";

export function hardcoverUrl(item: HardcoverLinkable): string {
  const slug = typeof item.hardcoverSlug === "string" ? item.hardcoverSlug.trim() : "";
  if (slug) return `${BASE}/books/${encodeURIComponent(slug)}`;

  const id = item.hardcoverId;
  if (id !== undefined && id !== null && String(id).trim()) {
    return `${BASE}/books/${encodeURIComponent(String(id).trim())}`;
  }

  const q = [item.title, item.authors].filter(Boolean).join(" ").trim();
  return `${BASE}/search?q=${encodeURIComponent(q)}`;
}

/** Open a book's Hardcover page in a new, isolated tab. */
export function openHardcover(item: HardcoverLinkable): void {
  window.open(hardcoverUrl(item), "_blank", "noopener,noreferrer");
}
