import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const account = {
  id: "0199ed3b-c950-7000-8000-000000000018",
  provider: "google",
  displayName: "Personal",
  syncState: "idle",
  disabledAt: null,
};

test("live inbox virtualizes large account-scoped pages and preserves selection", async ({
  page,
}, testInfo) => {
  // Exercise the 100k acceptance target once; responsive projects use a
  // smaller page so the six-project suite does not duplicate a large fixture.
  const itemCount = testInfo.project.name === "desktop-large" ? 100_000 : 500;
  const syntheticThreads = Array.from({ length: itemCount }, (_, index) => ({
    id: `10000000-0000-7000-8000-${index.toString(16).padStart(12, "0")}`,
    accountId: account.id,
    senderName: `Sender ${index}`,
    senderAddress: `sender-${index}@example.test`,
    subject: "",
    preview: "",
    lastMessageAt: "2026-09-07T17:00:00Z",
    isRead: index > 0,
    isStarred: false,
    isImportant: false,
    category: "primary",
    messageCount: 1,
    attachmentCount: index === 0 ? 1 : 0,
  }));
  let remoteImageRequests = 0;
  let searchRequests = 0;
  const actionRequests: Array<{ key: string | null; body: unknown }> = [];
  const draftRequests: Array<{ method: string; url: string; body: unknown }> = [];
  const sendRequests: Array<{ key: string | null; body: unknown }> = [];
  let attachmentDownloads = 0;
  let attachmentUploads = 0;
  let draftRevision = 1;
  let draftAttachments: Array<Record<string, unknown>> = [];
  const draftResponse = () => ({
    id: "50000000-0000-7000-8000-000000000001",
    accountId: account.id,
    subject: "Browser draft",
    bodyText: "Safe browser body",
    bodyHtml: "<p>Safe browser body</p>",
    recipients: [{ role: "to", position: 0, displayName: null, address: "recipient@example.test" }],
    attachments: draftAttachments,
    mode: "new",
    sourceMessageId: null,
    localRevision: draftRevision,
    syncedRevision: 0,
    syncStatus: "queued",
    remoteCheckpointAt: "2026-09-07T18:00:15Z",
    createdAt: "2026-09-07T18:00:00Z",
    updatedAt: "2026-09-07T18:00:00Z",
  });
  await page.route("**/api/auth/setup/status", (route) =>
    route.fulfill({ json: { configured: true } }),
  );
  await page.route("**/api/auth/get-session", (route) =>
    route.fulfill({ json: { user: { id: "test-owner" } } }),
  );
  await page.route("**/api/auth/token", (route) => route.fulfill({ json: { token: "test-jwt" } }));
  await page.route("**/api/v1/translations/en", (route) =>
    route.fulfill({
      json: {
        locale: "en",
        defaultLocale: "en",
        revision: 1,
        messages: {},
        missingKeys: [],
      },
    }),
  );
  await page.route("**/api/v1/accounts", (route) => route.fulfill({ json: { items: [account] } }));
  await page.route("**/api/v1/mailboxes?**", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "mailbox-1",
            accountId: account.id,
            remoteName: "Inbox",
            localName: null,
            role: "inbox",
            totalCount: itemCount,
            unreadCount: 1,
          },
        ],
      },
    }),
  );
  await page.route("**/api/v1/labels?**", (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: "label-1",
            accountId: account.id,
            remoteName: "Primary",
            localName: null,
            kind: "category",
            category: "primary",
            color: null,
            totalCount: itemCount,
            unreadCount: 1,
          },
        ],
      },
    }),
  );
  await page.route("**/api/v1/threads?**", (route) => {
    const category = new URL(route.request().url()).searchParams.get("category");
    return route.fulfill({
      json: { items: category === "primary" ? syntheticThreads : [], nextCursor: null },
    });
  });
  await page.route("**/api/v1/threads/*?**", (route) =>
    route.fulfill({
      json: {
        thread: {
          id: syntheticThreads[0]?.id,
          accountId: account.id,
          category: "primary",
          messageCount: 1,
        },
        messages: [
          {
            id: "20000000-0000-7000-8000-000000000001",
            threadId: syntheticThreads[0]?.id,
            accountId: account.id,
            subject: "",
            bodyText: "",
            bodyHtml: '<img data-mailflow-src="https://images.example.test/pixel" alt="">',
            sentAt: "2026-09-07T17:00:00Z",
            isRead: false,
            isStarred: false,
            isImportant: false,
            addresses: [
              {
                role: "from",
                position: 0,
                displayName: "Sender 0",
                address: "sender-0@example.test",
              },
            ],
            attachments: [
              {
                id: "70000000-0000-7000-8000-000000000001",
                position: 0,
                filename: "fixture.txt",
                mediaType: "text/plain",
                disposition: "attachment",
                sizeBytes: 8,
              },
            ],
          },
        ],
        nextCursor: null,
      },
    }),
  );
  await page.route("**/api/v1/search?**", (route) => {
    searchRequests += 1;
    return route.fulfill({
      json: {
        items: [
          {
            id: "30000000-0000-7000-8000-000000000001",
            threadId: syntheticThreads[0]?.id,
            accountId: account.id,
            senderName: "Search Sender",
            senderAddress: "search-sender@example.test",
            subject: "Quarterly result",
            preview: "Matched local search content",
            sentAt: "2026-09-07T17:00:00Z",
            isRead: true,
            isStarred: false,
            isImportant: false,
            hasAttachment: false,
            attachmentCount: 0,
            rank: 0.8,
          },
        ],
        nextCursor: null,
      },
    });
  });
  await page.route("**/api/v1/actions", async (route) => {
    actionRequests.push({
      key: route.request().headers()["idempotency-key"] ?? null,
      body: route.request().postDataJSON(),
    });
    return route.fulfill({
      status: 202,
      json: {
        items: [
          {
            targetId: syntheticThreads[0]?.id,
            actionId: "40000000-0000-7000-8000-000000000001",
            status: "pending",
            created: true,
          },
        ],
        partial: false,
      },
    });
  });
  await page.route("**/api/v1/drafts**", async (route) => {
    const request = route.request();
    const body = request.postData() ? request.postDataJSON() : null;
    if (Array.isArray(body?.attachments)) draftAttachments = body.attachments;
    draftRequests.push({ method: request.method(), url: request.url(), body });
    if (request.method() === "PUT") draftRevision += 1;
    if (request.url().endsWith("/checkpoint")) {
      return route.fulfill({
        json: { ...draftResponse(), syncedRevision: draftRevision, syncStatus: "synced" },
      });
    }
    return route.fulfill({
      status: request.method() === "POST" ? 201 : 200,
      json: draftResponse(),
    });
  });
  await page.route("**/api/v1/attachments", async (route) => {
    attachmentUploads += 1;
    return route.fulfill({
      status: 201,
      json: {
        objectId: "abcdef0123456789abcdef0123456789",
        filename: "upload.txt",
        mediaType: "text/plain",
        sizeBytes: 8,
      },
    });
  });
  await page.route("**/api/v1/attachments/*", async (route) => {
    attachmentDownloads += 1;
    return route.fulfill({
      status: 200,
      body: "mailflow",
      headers: { "content-type": "text/plain", "content-length": "8" },
    });
  });
  await page.route("**/api/v1/send", async (route) => {
    sendRequests.push({
      key: route.request().headers()["idempotency-key"] ?? null,
      body: route.request().postDataJSON(),
    });
    return route.fulfill({
      status: 202,
      json: {
        id: "60000000-0000-7000-8000-000000000001",
        accountId: account.id,
        draftId: "50000000-0000-7000-8000-000000000001",
        status: "sent",
        remoteId: "remote-message",
      },
    });
  });
  await page.route("https://images.example.test/**", (route) => {
    remoteImageRequests += 1;
    return route.abort();
  });

  await page.goto("/");
  await expect(page.getByText("Sender 0", { exact: true })).toBeVisible({ timeout: 10_000 });
  expect(await page.locator(".message-row").count()).toBeLessThan(100);

  await page.getByRole("button", { name: "Select Sender 0" }).click();
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("button", { name: "Select Sender 0" })).toHaveClass(/checked/);

  await page.getByRole("button", { name: "Open Sender 0" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "(No subject)" })).toBeVisible();
  const conversationAccessibility = await new AxeBuilder({ page })
    .include(".conversation")
    .exclude(".safe-mail-frame")
    .analyze();
  expect(
    conversationAccessibility.violations.filter(
      (violation) => violation.impact === "critical" || violation.impact === "serious",
    ),
  ).toEqual([]);
  expect(remoteImageRequests).toBe(0);
  await page.getByRole("button", { name: "Display remote images" }).click();
  await expect.poll(() => remoteImageRequests).toBeGreaterThan(0);
  await page.getByRole("button", { name: "Download attachment" }).click();
  await expect.poll(() => attachmentDownloads).toBe(1);
  await page.getByRole("button", { name: "Reply", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Recipients" })).toHaveValue(
    "sender-0@example.test",
  );
  await expect(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Re: ");
  await page.getByRole("button", { name: "Discard draft" }).click();
  await page.getByRole("button", { name: "Forward", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Recipients" })).toHaveValue("");
  await expect(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Fwd: ");
  await page.getByRole("button", { name: "Discard draft" }).click();
  await page.getByRole("button", { name: "Back to inbox" }).click();

  const search = page.getByRole("combobox", { name: "Search mail" });
  await search.fill("after:yesterday");
  await search.press("Enter");
  await expect(page.getByRole("alert")).toContainText("invalid");
  expect(searchRequests).toBe(0);
  await search.fill("quarterly from:search-sender@example.test");
  await search.press("Enter");
  await expect(page.getByText("Search Sender", { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/q=quarterly\+from%3Asearch-sender%40example\.test/u);
  expect(searchRequests).toBe(1);
  await page.getByRole("button", { name: "Mark unread" }).first().click();
  await expect.poll(() => actionRequests.length).toBe(1);
  expect(actionRequests[0]?.key).toMatch(/^mailflow-/u);
  expect(actionRequests[0]?.body).toMatchObject({
    accountId: account.id,
    kind: "mark_unread",
    targetIds: [syntheticThreads[0]?.id],
  });
  await search.press("Escape");
  await expect(page.getByText("Sender 0", { exact: true })).toBeVisible();

  await page.getByRole("tab", { name: "Promotions" }).click();
  await expect(page.getByText("No messages here")).toBeVisible();

  const composeButton = page.getByRole("button", { name: "Compose" });
  if (!(await composeButton.isVisible())) {
    await page.getByRole("button", { name: "Toggle navigation" }).click();
  }
  await composeButton.click();
  await page.getByRole("button", { name: "Minimize" }).click();
  await expect(page.getByRole("textbox", { name: "Subject" })).toHaveCount(0);
  await page.getByRole("button", { name: "Minimize" }).click();
  await page.getByRole("button", { name: "Maximize" }).click();
  await expect(page.getByRole("region", { name: "New message" })).toHaveClass(/maximized/u);
  await page.getByRole("button", { name: "Maximize" }).click();
  await page.getByRole("textbox", { name: "Recipients" }).fill("recipient@example.test");
  await page.getByRole("textbox", { name: "Subject" }).fill("Browser draft");
  await page.getByRole("textbox", { name: "Write a message" }).fill("Safe browser body");
  await page.locator('.compose-panel input[type="file"]').setInputFiles({
    name: "upload.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("mailflow"),
  });
  await expect(page.getByText("upload.txt", { exact: true })).toBeVisible();
  await expect.poll(() => attachmentUploads).toBe(1);
  const composerAccessibility = await new AxeBuilder({ page }).include(".compose-panel").analyze();
  expect(
    composerAccessibility.violations.filter(
      (violation) => violation.impact === "critical" || violation.impact === "serious",
    ),
  ).toEqual([]);
  await expect(page.getByText("Saved", { exact: true })).toBeVisible({ timeout: 5_000 });
  expect(draftRequests.some((request) => request.method === "POST")).toBe(true);
  expect(
    draftRequests.some(
      (request) =>
        Array.isArray((request.body as { attachments?: unknown })?.attachments) &&
        (request.body as { attachments: Array<{ objectId?: string }> }).attachments[0]?.objectId ===
          "abcdef0123456789abcdef0123456789",
    ),
  ).toBe(true);
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect(page.getByRole("region", { name: "New message" })).toHaveCount(0);
  expect(draftRequests.some((request) => request.url.endsWith("/checkpoint"))).toBe(true);

  if (!(await composeButton.isVisible())) {
    await page.getByRole("button", { name: "Toggle navigation" }).click();
  }
  await composeButton.click();
  await expect(page.getByRole("textbox", { name: "Subject" })).toHaveValue("Browser draft");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.getByRole("region", { name: "New message" })).toHaveCount(0);
  expect(sendRequests).toHaveLength(1);
  expect(sendRequests[0]?.key).toMatch(/^mailflow-send-/u);
  expect(sendRequests[0]?.body).toMatchObject({
    accountId: account.id,
    expectedRevision: draftRevision,
  });

  await page.evaluate(() => window.dispatchEvent(new Event("offline")));
  await expect(page.getByText("Showing is paused until the connection returns.")).toBeVisible();
});
