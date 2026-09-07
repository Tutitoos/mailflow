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
  await page.route("**/api/auth/setup/status", (route) =>
    route.fulfill({ json: { configured: true } }),
  );
  await page.route("**/api/auth/get-session", (route) =>
    route.fulfill({ json: { user: { id: "test-owner" } } }),
  );
  await page.route("**/api/auth/token", (route) => route.fulfill({ json: { token: "test-jwt" } }));
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
            attachments: [],
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
  await search.press("Escape");
  await expect(page.getByText("Sender 0", { exact: true })).toBeVisible();

  await page.getByRole("tab", { name: "Promotions" }).click();
  await expect(page.getByText("No messages here")).toBeVisible();

  await page.evaluate(() => window.dispatchEvent(new Event("offline")));
  await expect(page.getByText("Showing is paused until the connection returns.")).toBeVisible();
});
