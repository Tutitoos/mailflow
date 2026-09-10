import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test, vi } from "vitest";
import { ConversationView } from "./conversation";

function renderConversation(labelId?: string) {
  return renderToStaticMarkup(
    <ConversationView
      accountId="account-1"
      threadId="thread-1"
      locale="en"
      onBack={vi.fn()}
      onCompose={vi.fn()}
      onAction={vi.fn()}
      canActions
      canCompose
      canAttachments
      labelId={labelId}
    />,
  );
}

describe("ConversationView label action", () => {
  test("offers Labels only when an applicable label exists", () => {
    expect(renderConversation("label-1")).toContain('aria-label="Labels"');
    expect(renderConversation()).not.toContain('aria-label="Labels"');
  });
});
