// hardcover.test.ts — the Hardcover deep-link builder (gaka-qic0). The DTO has
// no slug/id today, so the contract that matters is the SEARCH fallback and its
// encoding; the direct-page branches are covered so they're already correct the
// day the DTO grows those fields.
import { describe, expect, it } from "vitest";
import { hardcoverUrl } from "./hardcover";

describe("hardcoverUrl", () => {
  it("falls back to a title+author search when no slug/id is present", () => {
    expect(
      hardcoverUrl({ title: "The Way of Kings", authors: "Brandon Sanderson" }),
    ).toBe(
      "https://hardcover.app/search?q=" +
        encodeURIComponent("The Way of Kings Brandon Sanderson"),
    );
  });

  it("URL-encodes reserved characters in the search query", () => {
    const url = hardcoverUrl({ title: "Q&A: Tom's Guide", authors: "A. B/C" });
    expect(url.startsWith("https://hardcover.app/search?q=")).toBe(true);
    expect(url).not.toContain("&A"); // the & is encoded, not a query separator
    expect(url).toContain(encodeURIComponent("Q&A: Tom's Guide A. B/C"));
  });

  it("omits a missing author cleanly (no trailing space)", () => {
    expect(hardcoverUrl({ title: "Solo", authors: "" })).toBe(
      "https://hardcover.app/search?q=" + encodeURIComponent("Solo"),
    );
  });

  it("prefers a direct book page when a hardcover slug is present", () => {
    expect(
      hardcoverUrl({
        title: "Dune",
        authors: "Frank Herbert",
        hardcoverSlug: "dune",
      }),
    ).toBe("https://hardcover.app/books/dune");
  });

  it("uses the hardcover id when a slug is absent", () => {
    expect(
      hardcoverUrl({ title: "Dune", authors: "Frank Herbert", hardcoverId: 12345 }),
    ).toBe("https://hardcover.app/books/12345");
  });

  it("links direct to the book page when a resolved hardcoverBookId is present", () => {
    expect(
      hardcoverUrl({
        title: "Dune",
        authors: "Frank Herbert",
        hardcoverBookId: 987654,
        externalId: "B0ASIN123", // present, but the resolved id wins
      }),
    ).toBe("https://hardcover.app/books/987654");
  });

  it("falls back to an ASIN-precise search when only an ASIN (external_id) is present", () => {
    expect(
      hardcoverUrl({
        title: "Project Hail Mary",
        authors: "Andy Weir",
        externalId: "B08GB58KD5",
      }),
    ).toBe("https://hardcover.app/search?q=B08GB58KD5");
  });

  it("prefers amazonAsin over external_id for the ASIN search", () => {
    expect(
      hardcoverUrl({
        title: "Project Hail Mary",
        authors: "Andy Weir",
        externalId: "B0AUDIOASN",
        amazonAsin: "B0PRINTASN",
      }),
    ).toBe("https://hardcover.app/search?q=B0PRINTASN");
  });

  it("still uses the title+author search when neither an id nor an ASIN exists", () => {
    expect(
      hardcoverUrl({ title: "Solo", authors: "", externalId: "", amazonAsin: "" }),
    ).toBe("https://hardcover.app/search?q=" + encodeURIComponent("Solo"));
  });
});
