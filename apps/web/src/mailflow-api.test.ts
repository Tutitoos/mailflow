import { afterEach, describe, expect, it, vi } from "vitest";
import {
  checkpointDraft,
  createDraft,
  createGoogleAuthorization,
  createMailActions,
  createMicrosoftAuthorization,
  disconnectAccount,
  loadAccountConnections,
  loadConversationPage,
  loadGoogleAccounts,
  loadInboxPage,
  loadMailNavigation,
  type MailDraft,
  searchMail,
  sendDraft,
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

  it("starts Microsoft OAuth with an explicit re-consent request", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ token: "short-jwt" }))
      .mockResolvedValueOnce(
        json({
          authorizationUrl: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
        }),
      );
    vi.stubGlobal("fetch", fetch);

    const authorizationUrl = await createMicrosoftAuthorization(true);

    expect(authorizationUrl).toContain("login.microsoftonline.com");
    expect(JSON.parse(String(fetch.mock.calls[1]?.[1]?.body))).toEqual({ reconsent: true });
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

  it("loads Google and Microsoft account capabilities without including IMAP", async () => {
    const fetch = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith("/api/auth/token")) return json({ token: "short-jwt" });
      if (url.includes("/oauth/google/status"))
        return json({ configured: true, setup: "docs/providers/google.md" });
      if (url.includes("/oauth/microsoft/status"))
        return json({ configured: true, setup: "docs/providers/microsoft.md" });
      return json({
        items: [
          {
            id: "google",
            provider: "google",
            displayName: "Personal",
            syncState: "idle",
            disabledAt: null,
          },
          {
            id: "microsoft",
            provider: "microsoft",
            displayName: "Work",
            syncState: "idle",
            disabledAt: null,
          },
          {
            id: "imap",
            provider: "imap",
            displayName: "Later",
            syncState: "idle",
            disabledAt: null,
          },
        ],
      });
    });
    vi.stubGlobal("fetch", fetch);

    const result = await loadAccountConnections();

    expect(result.status.microsoft.configured).toBe(true);
    expect(result.accounts.map((account) => account.provider)).toEqual(["google", "microsoft"]);
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

  it("saves, checkpoints, and sends a revisioned draft with one delivery key", async () => {
    const draft: MailDraft = {
      id: "draft-1",
      accountId: "account-1",
      subject: "Fixture",
      bodyText: "Hello",
      bodyHtml: "<p>Hello</p>",
      recipients: [{ role: "to", address: "recipient@example.test" }],
      attachments: [],
      mode: "new",
      localRevision: 1,
      syncedRevision: 0,
      syncStatus: "queued",
      remoteCheckpointAt: "2026-09-07T18:00:15Z",
      createdAt: "2026-09-07T18:00:00Z",
      updatedAt: "2026-09-07T18:00:00Z",
    };
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ token: "mail-jwt" }))
      .mockResolvedValueOnce(json(draft, 201))
      .mockResolvedValueOnce(json({ token: "mail-jwt" }))
      .mockResolvedValueOnce(json({ ...draft, syncedRevision: 1, syncStatus: "synced" }))
      .mockResolvedValueOnce(json({ token: "mail-jwt" }))
      .mockResolvedValueOnce(
        json(
          {
            id: "delivery-1",
            accountId: "account-1",
            draftId: "draft-1",
            status: "sent",
            remoteId: "remote-1",
          },
          202,
        ),
      );
    vi.stubGlobal("fetch", fetch);

    const saved = await createDraft(draft);
    await checkpointDraft("account-1", saved.id);
    const delivery = await sendDraft(
      "account-1",
      saved.id,
      saved.localRevision,
      "mailflow-send-key-0001",
    );

    expect(delivery.status).toBe("sent");
    expect(fetch.mock.calls[1]?.[0]).toBe("/api/v1/drafts");
    expect(fetch.mock.calls[3]?.[0]).toBe("/api/v1/drafts/draft-1/checkpoint");
    expect(fetch.mock.calls[5]?.[1]?.headers).toMatchObject({
      authorization: "Bearer mail-jwt",
      "Idempotency-Key": "mailflow-send-key-0001",
    });
  });
});
