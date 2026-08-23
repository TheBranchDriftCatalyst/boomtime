// UserAvatarImage.test.tsx — non-tautological coverage of the per-user
// chibi avatar <img> with initials fallback (boom-9v4).
//
// Invariants under test:
//   - src is built from /api/v1/users/{username}/avatar and URL-encodes
//     usernames with special chars.
//   - bustHint appends ?v=<encoded value>.
//   - onError swaps in the initials fallback with the correct 1-2 char
//     glyph derived from the username's leading ASCII letters.
//   - Single-letter usernames render a single-letter glyph (not padded).
import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { UserAvatarImage } from "./UserAvatarImage";

describe("UserAvatarImage", () => {
  it("builds the src from the username", () => {
    render(<UserAvatarImage username="panda" />);
    const img = screen.getByTestId("user-avatar-image") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe("/api/v1/users/panda/avatar");
  });

  it("URL-encodes usernames with special chars", () => {
    render(<UserAvatarImage username="the.branch/drift" />);
    const img = screen.getByTestId("user-avatar-image") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe(
      "/api/v1/users/the.branch%2Fdrift/avatar",
    );
  });

  it("appends ?v=<bustHint> when provided", () => {
    render(<UserAvatarImage username="panda" bustHint={17530001} />);
    const img = screen.getByTestId("user-avatar-image") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe(
      "/api/v1/users/panda/avatar?v=17530001",
    );
  });

  it("falls back to initials on error (404 / not-ready)", () => {
    render(<UserAvatarImage username="panda" />);
    fireEvent.error(screen.getByTestId("user-avatar-image"));
    expect(screen.queryByTestId("user-avatar-image")).not.toBeInTheDocument();
    const fb = screen.getByTestId("user-avatar-fallback");
    expect(fb).toHaveTextContent("PA");
  });

  it("renders a single-letter fallback for one-char usernames", () => {
    render(<UserAvatarImage username="x" />);
    fireEvent.error(screen.getByTestId("user-avatar-image"));
    const fb = screen.getByTestId("user-avatar-fallback");
    expect(fb).toHaveTextContent("X");
    // Regression guard: MUST NOT pad to "X?" or duplicate.
    expect(fb.textContent).toBe("X");
  });

  it("skips non-letter chars when deriving initials", () => {
    render(<UserAvatarImage username="1234-branch" />);
    fireEvent.error(screen.getByTestId("user-avatar-image"));
    const fb = screen.getByTestId("user-avatar-fallback");
    expect(fb).toHaveTextContent("BR");
  });
});
