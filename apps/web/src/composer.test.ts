import { describe, expect, it } from "vitest";
import { DRAFT_LOCAL_SAVE_MS, DRAFT_REMOTE_CHECKPOINT_MS } from "./composer";

describe("composer timing contract", () => {
  it("uses the documented local debounce and remote checkpoint interval", () => {
    expect(DRAFT_LOCAL_SAVE_MS).toBe(2_000);
    expect(DRAFT_REMOTE_CHECKPOINT_MS).toBe(15_000);
  });
});
