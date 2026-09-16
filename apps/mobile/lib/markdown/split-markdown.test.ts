// @vitest-environment node
import { describe, expect, it } from "vitest";
import { splitMarkdown } from "./split-markdown";

describe("splitMarkdown", () => {
  it("trims prose edges without changing internal whitespace", () => {
    expect(splitMarkdown("  hello   world  ")).toEqual([
      { type: "prose", content: "hello   world" },
    ]);
  });

  it("returns no segments for empty input", () => {
    expect(splitMarkdown("")).toEqual([]);
  });
});
