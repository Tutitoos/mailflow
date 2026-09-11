import { describe, expect, it } from "vitest";
import type { MailLabel } from "./mailflow-api";
import { buildLabelTree } from "./pages";

const label = (id: string, name: string, unreadCount = 0): MailLabel => ({
  id,
  accountId: "account-1",
  remoteName: name,
  localName: null,
  kind: "user",
  category: null,
  color: null,
  totalCount: unreadCount,
  unreadCount,
});

describe("buildLabelTree", () => {
  it("groups slash-delimited labels beneath one collapsible prefix", () => {
    const tree = buildLabelTree([
      label("jobs", "Buzones/Jobs", 2),
      label("root", "Buzones"),
      label("dev", "Buzones/Developer", 3),
      label("smx", "SMX"),
    ]);

    expect(tree.map(({ name }) => name)).toEqual(["Buzones", "SMX"]);
    expect(tree[0]?.label?.id).toBe("root");
    expect(tree[0]?.children.map(({ name }) => name)).toEqual(["Developer", "Jobs"]);
    expect(tree[0]?.children[0]?.label?.unreadCount).toBe(3);
  });
});
