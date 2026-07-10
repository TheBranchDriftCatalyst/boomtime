import { describe, expect, it } from "vitest";
import { shortPath } from "@/lib/pathLabel";

describe("shortPath", () => {
  it("shortens to parent/filename", () => {
    expect(shortPath("/a/b/c/file.ts")).toBe("c/file.ts");
    expect(shortPath("src/components/Foo.tsx")).toBe("components/Foo.tsx");
  });

  it("returns just the filename when there's no parent", () => {
    expect(shortPath("file.ts")).toBe("file.ts");
    expect(shortPath("/file.ts")).toBe("file.ts");
  });

  it("ignores trailing/duplicate slashes", () => {
    expect(shortPath("a//b//c.ts")).toBe("b/c.ts");
  });

  it("handles an empty string", () => {
    expect(shortPath("")).toBe("");
  });
});
