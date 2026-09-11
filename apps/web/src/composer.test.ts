import { describe, expect, it } from "vitest";
import {
  DRAFT_LOCAL_SAVE_MS,
  DRAFT_REMOTE_CHECKPOINT_MS,
  hasMeaningfulDraftContent,
} from "./composer";

describe("composer timing contract", () => {
  it("uses the documented local debounce and remote checkpoint interval", () => {
    expect(DRAFT_LOCAL_SAVE_MS).toBe(2_000);
    expect(DRAFT_REMOTE_CHECKPOINT_MS).toBe(15_000);
  });
});

describe("composer content guard", () => {
  const emptyDraft = {
    recipients: [],
    attachments: [],
    subject: "",
    bodyText: "",
  };

  it("does not treat whitespace-only draft fields as meaningful", () => {
    expect(hasMeaningfulDraftContent(emptyDraft)).toBe(false);
    expect(hasMeaningfulDraftContent({ ...emptyDraft, subject: "  ", bodyText: "\n" })).toBe(false);
  });

  it.each([
    { ...emptyDraft, recipients: [{ role: "to" as const, address: "person@example.test" }] },
    { ...emptyDraft, subject: "Subject" },
    { ...emptyDraft, bodyText: "Message" },
    {
      ...emptyDraft,
      attachments: [
        {
          objectId: "abcdef0123456789abcdef0123456789",
          filename: "file.txt",
          mediaType: "text/plain",
          sizeBytes: 4,
        },
      ],
    },
  ])("recognizes meaningful draft content", (draft) => {
    expect(hasMeaningfulDraftContent(draft)).toBe(true);
  });
});
