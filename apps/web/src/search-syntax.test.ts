import { describe, expect, it } from "vitest";
import { validateSearchSyntax } from "./search-syntax";

describe("search syntax", () => {
  it("accepts the documented operators and quoted values", () => {
    expect(
      validateSearchSyntax(
        'quarterly from:sender@example.test subject:"Project Atlas" after:2026-01-01 before:2026-02-01 has:attachment is:unread is:starred label:work in:inbox',
      ),
    ).toBeNull();
  });

  it.each([
    ["", "empty_query"],
    ["unknown:value", "unsupported_operator"],
    ['subject:"unfinished', "unclosed_quote"],
    ["from:", "missing_value"],
    ["after:yesterday", "invalid_value"],
    ["after:2026-02-01 before:2026-01-01", "invalid_range"],
  ])("returns a stable error for %s", (query, code) => {
    expect(validateSearchSyntax(query)).toBe(code);
  });
});
