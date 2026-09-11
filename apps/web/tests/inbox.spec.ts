import AxeBuilder from "@axe-core/playwright";
import { expect, test, type WebSocketRoute } from "@playwright/test";

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
  testInfo.setTimeout(90_000);
  // Exercise the 100k acceptance target once; responsive projects use a
  // smaller page so the six-project suite does not duplicate a large fixture.
  const itemCount = testInfo.project.name === "desktop-large" ? 100_000 : 500;
  const syntheticThreads = Array.from({ length: itemCount }, (_, index) => ({
    id: `10000000-0000-7000-8000-${index.toString(16).padStart(12, "0")}`,
    accountId: account.id,
    senderName: index === 1 ? "" : `Sender ${index}`,
    senderAddress: index === 1 ? "" : `sender-${index}@example.test`,
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
  let searchResultStarred = false;
  const searchStarredResponses: boolean[] = [];
  const openedThreadIds: string[] = [];
  let eventSocket: WebSocketRoute | undefined;
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
  await page.routeWebSocket("**/api/v1/events", (socket) => {
    eventSocket = socket;
  });
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
          {
            id: "mailbox-trash",
            accountId: account.id,
            remoteName: "Trash",
            localName: null,
            role: "trash",
            totalCount: 2,
            unreadCount: 0,
          },
          {
            id: "mailbox-junk",
            accountId: account.id,
            remoteName: "Spam",
            localName: null,
            role: "junk",
            totalCount: 1,
            unreadCount: 0,
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
          {
            id: "label-user-1",
            accountId: account.id,
            remoteName: "Buzones",
            localName: null,
            kind: "user",
            category: null,
            color: null,
            totalCount: 0,
            unreadCount: 0,
          },
          {
            id: "label-user-2",
            accountId: account.id,
            remoteName: "Buzones/Developer",
            localName: null,
            kind: "user",
            category: null,
            color: null,
            totalCount: 3,
            unreadCount: 3,
          },
          {
            id: "label-user-3",
            accountId: account.id,
            remoteName: "Buzones/Jobs",
            localName: null,
            kind: "user",
            category: null,
            color: null,
            totalCount: 0,
            unreadCount: 0,
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
  await page.route("**/api/v1/threads/*?**", (route) => {
    openedThreadIds.push(new URL(route.request().url()).pathname.split("/").at(-1) ?? "");
    return route.fulfill({
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
    });
  });
  await page.route("**/api/v1/search?**", (route) => {
    searchRequests += 1;
    searchStarredResponses.push(searchResultStarred);
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
            isStarred: searchResultStarred,
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
  await expect(page.getByText("Sender 0", { exact: true })).toBeVisible({ timeout: 20_000 });
  await expect.poll(() => Boolean(eventSocket)).toBe(true);
  expect(await page.locator(".message-row").count()).toBeLessThan(100);
  const visualRegressionProjects = new Set(["desktop-large", "tablet-portrait", "mobile"]);
  if (visualRegressionProjects.has(testInfo.project.name)) {
    await expect(page).toHaveScreenshot(`inbox-${testInfo.project.name}-en.png`, {
      animations: "disabled",
      caret: "hide",
      maxDiffPixelRatio: 0.05,
    });
  }

  await page.getByRole("button", { name: "Account menu" }).click();
  await expect(page.getByRole("menu", { name: "Accounts" })).toContainText("Personal");
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "EN", exact: true }).click();
  await expect(page.getByRole("menu", { name: "Language" })).toBeVisible();
  await expect(page.getByRole("menuitemradio", { name: "EN English" })).toHaveAttribute(
    "aria-checked",
    "true",
  );
  await page.getByRole("menuitemradio", { name: "ES Spanish" }).click();
  await expect(page.getByRole("button", { name: "ES", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Alternar navegación" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Notificaciones" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Menú de cuenta" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Más acciones" })).toBeVisible();
  await expect(page.locator("time").first()).toContainText("sept");
  if (visualRegressionProjects.has(testInfo.project.name)) {
    await expect(page).toHaveScreenshot(`inbox-${testInfo.project.name}-es.png`, {
      animations: "disabled",
      caret: "hide",
      maxDiffPixelRatio: 0.05,
    });
  }
  if ((page.viewportSize()?.width ?? 0) >= 1200) {
    await expect(page.getByRole("navigation", { name: "Buzones" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Recibidos", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Developer" })).toBeHidden();
    await page.getByRole("button", { name: "Expandir Buzones" }).click();
    await expect(page.getByRole("button", { name: "Developer" })).toBeVisible();
    await page.getByRole("button", { name: "Contraer Buzones" }).click();
    await page.getByRole("button", { name: "Más", exact: true }).click();
    await expect(page.getByRole("button", { name: "Papelera", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Spam", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Más", exact: true }).click();
  }
  await page.locator(".message-row").first().click({ button: "right" });
  await expect(page.getByRole("menu", { name: /Acciones para Sender 0/ })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "Abrir", exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "ES", exact: true }).click();
  await page.getByRole("menuitemradio", { name: "EN Inglés" }).click();

  await expect(page.getByRole("button", { name: "Close rail" })).toHaveCount(0);

  if ((page.viewportSize()?.width ?? 0) >= 1200) {
    await expect(page.getByRole("button", { name: "Developer" })).toBeHidden();
    await page.getByRole("button", { name: "Expand Buzones" }).click();
    await expect(page.getByRole("button", { name: "Developer" })).toBeVisible();
    await page.getByRole("button", { name: "Collapse Buzones" }).click();
    await expect(page.getByRole("button", { name: "Developer" })).toBeHidden();

    await page.getByRole("button", { name: "More", exact: true }).click();
    await expect(page.getByRole("button", { name: "Trash", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Spam", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "More", exact: true }).click();
    await expect(page.getByRole("button", { name: "Trash", exact: true })).toBeHidden();
  }

  await page.locator(".message-row").first().click({ button: "right" });
  await expect(page.getByRole("menu", { name: /Actions for Sender 0/ })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "Archive", exact: true })).toBeVisible();
  await page.getByRole("menuitem", { name: "Open", exact: true }).click();
  await expect.poll(() => openedThreadIds.at(-1)).toBe(syntheticThreads[0]?.id);
  await page.getByRole("button", { name: "Back to inbox" }).click();
  await expect(page.getByText("Sender 0", { exact: true })).toBeVisible();

  await page.keyboard.press("/");
  await expect(page.getByRole("combobox", { name: "Search mail" })).toBeFocused();
  await expect(page.getByRole("button", { name: "from:", exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  const reducedMotion = await page.evaluate(() => {
    const probe = document.createElement("div");
    probe.style.animationDuration = "1s";
    probe.style.transitionDuration = "1s";
    document.body.append(probe);
    const styles = getComputedStyle(probe);
    const result = {
      preferred: matchMedia("(prefers-reduced-motion: reduce)").matches,
      animationDurationSeconds: Number.parseFloat(styles.animationDuration),
      transitionDurationSeconds: Number.parseFloat(styles.transitionDuration),
    };
    probe.remove();
    return result;
  });
  expect(reducedMotion.preferred).toBe(true);
  expect(reducedMotion.animationDurationSeconds).toBeLessThanOrEqual(0.00001);
  expect(reducedMotion.transitionDurationSeconds).toBeLessThanOrEqual(0.00001);

  const firstSelection = page.getByRole("button", { name: "Select Sender 0" });
  await firstSelection.click();
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(firstSelection).toBeVisible({ timeout: 20_000 });
  await expect(firstSelection).toHaveClass(/checked/);
  await expect(page.locator(".message-row").first()).toContainText("(No subject)");
  await expect(page.locator(".message-row").first()).toContainText("No preview");
  await expect(page.locator(".message-row").first()).not.toContainText("— No preview");
  await expect(page.locator(".message-row").nth(1)).toContainText("Unknown sender");

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
  await page.getByRole("button", { name: "Labels", exact: true }).click();
  await expect.poll(() => actionRequests.length).toBe(1);
  expect(actionRequests[0]?.body).toMatchObject({
    accountId: account.id,
    kind: "add_label",
    targetIds: [syntheticThreads[0]?.id],
    labelId: "label-user-1",
  });
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
  searchResultStarred = true;
  eventSocket?.send(
    JSON.stringify({
      version: 1,
      cursor: "1-0",
      type: "mail.changed",
      timestamp: "2026-09-07T18:00:00Z",
      payload: { accountId: account.id },
    }),
  );
  await expect.poll(() => searchRequests).toBe(2);
  expect(searchStarredResponses.at(-1)).toBe(true);
  await expect(page.getByRole("button", { name: "Unstar Quarterly result" })).toBeVisible();
  searchResultStarred = false;
  eventSocket?.send(
    JSON.stringify({
      version: 1,
      cursor: "2-0",
      type: "mail.changed",
      timestamp: "2026-09-07T18:00:01Z",
      payload: { accountId: account.id },
    }),
  );
  await expect.poll(() => searchRequests).toBe(3);
  await expect(page.getByRole("button", { name: "Star Quarterly result" })).toBeVisible();
  await page.getByRole("button", { name: "Select Quarterly result" }).click();
  await page.getByRole("button", { name: "Mark unread" }).first().click();
  await expect.poll(() => actionRequests.length).toBe(2);
  expect(actionRequests[1]?.key).toMatch(/^mailflow-/u);
  expect(actionRequests[1]?.body).toMatchObject({
    accountId: account.id,
    kind: "mark_unread",
    targetIds: [syntheticThreads[0]?.id],
  });
  await expect(
    page.getByRole("button", { name: "Mark read Quarterly result", includeHidden: true }),
  ).toHaveCount(1);
  await page.getByRole("button", { name: "Select Quarterly result" }).click();
  await expect(page.getByRole("button", { name: "Mark read" }).first()).toBeVisible();
  await page.getByRole("button", { name: "Mark read" }).first().click();
  await expect.poll(() => actionRequests.length).toBe(3);
  expect(actionRequests[2]?.body).toMatchObject({
    accountId: account.id,
    kind: "mark_read",
    targetIds: [syntheticThreads[0]?.id],
  });
  await expect(
    page.getByRole("button", { name: "Mark unread Quarterly result", includeHidden: true }),
  ).toHaveCount(1);
  await search.press("Escape");
  await expect(page.getByText("Sender 0", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Select Sender 0" }).click();
  await page.keyboard.press("e");
  await expect.poll(() => actionRequests.length).toBe(4);
  expect(actionRequests[3]?.body).toMatchObject({
    accountId: account.id,
    kind: "archive",
    targetIds: [syntheticThreads[0]?.id],
  });

  await page.getByRole("tab", { name: "Promotions" }).click();
  await expect(page.getByText("No messages here")).toBeVisible();

  const composeButton = page.getByRole("button", { name: "Compose" });
  if (!(await composeButton.isVisible())) {
    await page.getByRole("button", { name: "Toggle navigation" }).click();
  }
  const draftsBeforeEmptyCompose = draftRequests.length;
  await composeButton.click();
  await page.waitForTimeout(2_100);
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect(page.getByRole("region", { name: "New message" })).toHaveCount(0);
  expect(draftRequests).toHaveLength(draftsBeforeEmptyCompose);

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
