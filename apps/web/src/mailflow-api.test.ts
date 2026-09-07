import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createGoogleAuthorization,
  createMailActions,
  disconnectAccount,
  loadConversationPage,
  loadGoogleAccounts,
  loadInboxPage,
  loadMailNavigation,
  searchMail,
} from "./mailflow-api";

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });

afterEach(() => vi.unstubAllGlobals());

describe("Mailflow API client", () => {
  it("uses a short Better Auth JWT and exposes only public account fields", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ token: "short-jwt" }))
      .mockResolvedValueOnce(json({ token: "short-jwt" }))
      .mockResolvedValueOnce(json({ configured: true, setup: "docs/providers/google.md" }))
      .mockResolvedValueOnce(
        json({
          items: [
            {
              id: "account-1",
              provider: "google",
              displayName: "Test",
              syncState: "idle",
              disabledAt: null,
            },
          ],
        }),
      );
    vi.stubGlobal("fetch", fetch);
    const result = await loadGoogleAccounts();
    expect(result.accounts).toHaveLength(1);
    expect(fetch.mock.calls[2]?.[1]?.headers).toMatchObject({ authorization: "Bearer short-jwt" });
  });

  it("starts OAuth and disconnects without receiving provider credentials", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(json({ token: "short-jwt" }))
        .mockResolvedValueOnce(
          json({ authorizationUrl: "https://accounts.example.test/authorize" }),
        )
        .mockResolvedValueOnce(json({ token: "short-jwt" }))
        .mockResolvedValueOnce(
          json({
            account: {
              id: "account-1",
              provider: "google",
              displayName: "Test",
              syncState: "disabled",
              disabledAt: "2026-09-07T00:00:00Z",
            },
            remoteRevoked: true,
          }),
        ),
    );
    const authorizationUrl = await createGoogleAuthorization();
    const result = await disconnectAccount("account-1");
    expect(authorizationUrl).toBe("https://accounts.example.test/authorize");
    expect(result.remoteRevoked).toBe(true);
  });

  it("loads account-scoped navigation and opaque inbox pages", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ token: "mail-jwt" }))
      .mockResolvedValueOnce(json({ token: "mail-jwt" }))
      .mockResolvedValueOnce(json({ items: [{ id: "mailbox-1", role: "inbox" }] }))
      .mockResolvedValueOnce(json({ items: [{ id: "label-1", category: "primary" }] }))
      .mockResolvedValueOnce(json({ token: "mail-jwt" }))
      .mockResolvedValueOnce(
        json({
          items: [
            {
              id: "thread-1",
              accountId: "account-1",
              senderName: "Fixture sender",
              senderAddress: "sender@example.test",
              subject: "",
              preview: "",
              lastMessageAt: "2026-09-07T17:00:00Z",
              isRead: false,
              isStarred: false,
              isImportant: false,
              category: "primary",
              messageCount: 1,
              attachmentCount: 0,
            },
          ],
          nextCursor: "opaque-cursor",
        }),
      )
      .mockResolvedValueOnce(json({ token: "mail-jwt" }))
      .mockResolvedValueOnce(
        json({
          thread: { id: "thread-1", accountId: "account-1", category: "primary", messageCount: 1 },
          messages: [],
          nextCursor: null,
        }),
      );
    vi.stubGlobal("fetch", fetch);

    const navigation = await loadMailNavigation("account-1");
    const page = await loadInboxPage("account-1", "primary", "previous-cursor");
    const conversation = await loadConversationPage("account-1", "thread-1");

    expect(navigation.mailboxes).toHaveLength(1);
    expect(page.nextCursor).toBe("opaque-cursor");
    expect(conversation.thread.id).toBe("thread-1");
    expect(fetch.mock.calls[2]?.[0]).toContain("accountId=account-1");
    expect(fetch.mock.calls[5]?.[0]).toContain("cursor=previous-cursor");
    expect(fetch.mock.calls[7]?.[0]).toContain("/threads/thread-1?accountId=account-1");
  });

  it("encodes account-scoped search expressions and cursors", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ token: "mail-jwt" }))
      .mockResolvedValueOnce(json({ items: [], nextCursor: "next-search" }));
    vi.stubGlobal("fetch", fetch);

    const page = await searchMail("account-1", "from:sender@example.test quarterly", "opaque");

    expect(page.nextCursor).toBe("next-search");
    const requestUrl = new URL(String(fetch.mock.calls[1]?.[0]), "https://mailflow.example.test");
    expect(requestUrl.pathname).toBe("/api/v1/search");
    expect(requestUrl.searchParams.get("accountId")).toBe("account-1");
    expect(requestUrl.searchParams.get("q")).toBe("from:sender@example.test quarterly");
    expect(requestUrl.searchParams.get("cursor")).toBe("opaque");
  });

  it("sends idempotent batch actions without provider data", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ token: "mail-jwt" }))
      .mockResolvedValueOnce(json({ items: [], partial: false }, 202));
    vi.stubGlobal("fetch", fetch);

    await createMailActions("account-1", "archive", ["thread-1"], "mailflow-action-key");

    expect(fetch.mock.calls[1]?.[1]?.headers).toMatchObject({
      authorization: "Bearer mail-jwt",
      "Idempotency-Key": "mailflow-action-key",
    });
    expect(JSON.parse(String(fetch.mock.calls[1]?.[1]?.body))).toEqual({
      accountId: "account-1",
      kind: "archive",
      targetIds: ["thread-1"],
    });
  });
});
