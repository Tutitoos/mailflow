export type SearchSyntaxError =
  | "empty_query"
  | "query_too_long"
  | "unclosed_quote"
  | "unsupported_operator"
  | "missing_value"
  | "invalid_value"
  | "duplicate_operator"
  | "invalid_range";

const operators = new Set(["from", "to", "subject", "after", "before", "has", "is", "label", "in"]);

export const searchSuggestions = [
  "from:",
  "to:",
  "subject:",
  "after:YYYY-MM-DD",
  "before:YYYY-MM-DD",
  "has:attachment",
  "is:unread",
  "is:starred",
  "label:",
  "in:",
];

function tokens(input: string): string[] | SearchSyntaxError {
  const result: string[] = [];
  let current = "";
  let quoted = false;
  for (const character of input) {
    if (character === '"') quoted = !quoted;
    if (/\s/u.test(character) && !quoted) {
      if (current) result.push(current);
      current = "";
    } else current += character;
  }
  if (quoted) return "unclosed_quote";
  if (current) result.push(current);
  return result;
}

export function validateSearchSyntax(input: string): SearchSyntaxError | null {
  if (!input.trim()) return "empty_query";
  if (new TextEncoder().encode(input).length > 1024) return "query_too_long";
  const parsed = tokens(input);
  if (typeof parsed === "string") return parsed;
  const singleton = new Set<string>();
  let after: string | undefined;
  let before: string | undefined;
  for (const token of parsed) {
    const separator = token.indexOf(":");
    if (separator < 0) continue;
    const operator = token.slice(0, separator).toLowerCase();
    const value = token
      .slice(separator + 1)
      .replace(/^"|"$/gu, "")
      .trim();
    if (!operators.has(operator)) return "unsupported_operator";
    if (!value) return "missing_value";
    if (operator === "after" || operator === "before") {
      if (singleton.has(operator)) return "duplicate_operator";
      singleton.add(operator);
      if (!/^\d{4}-\d{2}-\d{2}$/u.test(value) || Number.isNaN(Date.parse(`${value}T00:00:00Z`)))
        return "invalid_value";
      if (operator === "after") after = value;
      else before = value;
    }
    if (operator === "has" && value !== "attachment") return "invalid_value";
    if (operator === "is" && value !== "unread" && value !== "starred") return "invalid_value";
    if ((operator === "has" || operator === "is") && singleton.has(`${operator}:${value}`))
      return "duplicate_operator";
    singleton.add(`${operator}:${value}`);
  }
  if (after && before && after >= before) return "invalid_range";
  return null;
}
