import { describe, expect, it } from "vitest";
import { newUnreadThreads } from "./desktop-native";
import type { InboxThread } from "./mailflow-api";

function thread(id: string, isRead = false, lastMessageAt = "2026-09-09T10:00:00Z") {
  return {
    id,
    accountId: "018f6e6b-7c11-7d2c-8ce8-e8a98ef77b22",
    senderName: "Sanitized sender",
    senderAddress: "sender@example.test",
    subject: "Sanitized subject",
    preview: "Sanitized preview",
    lastMessageAt,
    isRead,
    isStarred: false,
    isImportant: false,
    category: "primary",
    messageCount: 1,
    attachmentCount: 0,
  } satisfies InboxThread;
}

describe("desktop new-mail selection", () => {
  it("uses the first snapshot as a baseline", () => {
    expect(newUnreadThreads(undefined, [thread("first")])).toEqual([]);
  });

  it("selects only unseen unread threads in newest-first order", () => {
    const current = [
      thread("known"),
      thread("read", true),
      thread("newer", false, "2026-09-09T12:00:00Z"),
      thread("new", false, "2026-09-09T11:00:00Z"),
    ];
    expect(newUnreadThreads(new Set(["known"]), current).map(({ id }) => id)).toEqual([
      "newer",
      "new",
    ]);
  });

  it("bounds a sync burst to three notifications", () => {
    const current = Array.from({ length: 6 }, (_, index) =>
      thread(`new-${index}`, false, `2026-09-09T1${index}:00:00Z`),
    );
    expect(newUnreadThreads(new Set(), current)).toHaveLength(3);
  });
});
