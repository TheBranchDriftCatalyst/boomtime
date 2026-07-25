// LabelImage.test.tsx — non-tautological coverage of the image-with-glyph-
// fallback component (gaka-myv).
//
// Invariants under test:
//   - src is built from the label id (URL-encoded)
//   - bustHint appends ?v=<encoded value>
//   - onError swaps in the fallback node (i.e., the glyph). We simulate the
//     error via React's onError handler because httptest isn't wired here.
import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { LabelImage } from "./LabelImage";

describe("LabelImage", () => {
  it("builds the src from the label id", () => {
    render(<LabelImage id="late-night-coder" fallback={<span>fb</span>} />);
    const img = screen.getByTestId("label-image") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe(
      "/api/v1/labels/late-night-coder/image",
    );
  });

  it("URL-encodes ids with special chars", () => {
    // Not a real label id, but the component should defend against injection.
    render(<LabelImage id="foo bar?x" fallback={<span>fb</span>} />);
    const img = screen.getByTestId("label-image") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe("/api/v1/labels/foo%20bar%3Fx/image");
  });

  it("appends ?v=<bustHint> when provided", () => {
    render(
      <LabelImage
        id="mac-native"
        bustHint={17530000}
        fallback={<span>fb</span>}
      />,
    );
    const img = screen.getByTestId("label-image") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe(
      "/api/v1/labels/mac-native/image?v=17530000",
    );
  });

  it("falls back to the glyph when the image errors", () => {
    render(
      <LabelImage
        id="unknown-label"
        fallback={<span data-testid="fallback-glyph">GLYPH</span>}
      />,
    );
    // Simulate a 404 onError.
    const img = screen.getByTestId("label-image");
    fireEvent.error(img);
    expect(screen.queryByTestId("label-image")).not.toBeInTheDocument();
    expect(screen.getByTestId("fallback-glyph")).toHaveTextContent("GLYPH");
  });

  it("renders the fallback state as null when the caller supplies null fallback", () => {
    // Used by HeroIdentity — no glyph in place, the row just collapses.
    render(<LabelImage id="unknown" fallback={null} />);
    fireEvent.error(screen.getByTestId("label-image"));
    expect(screen.queryByTestId("label-image")).not.toBeInTheDocument();
    // No fallback element present — that's the intended clean-empty behavior.
  });
});
