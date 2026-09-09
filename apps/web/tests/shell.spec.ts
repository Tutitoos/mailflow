import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

test("mail shell and admin remain operable", async ({ page }) => {
  await page.route("**/api/auth/setup/status", (route) =>
    route.fulfill({ json: { configured: true } }),
  );
  await page.route("**/api/auth/get-session", (route) =>
    route.fulfill({ json: { user: { id: "test-owner" } } }),
  );
  await page.route("**/api/auth/token", (route) => route.fulfill({ json: { token: "test-jwt" } }));
  await page.route("**/api/v1/oauth/google/status", (route) =>
    route.fulfill({ json: { configured: true, setup: "docs/providers/google.md" } }),
  );
  await page.route("**/api/v1/oauth/microsoft/status", (route) =>
    route.fulfill({ json: { configured: true, setup: "docs/providers/microsoft.md" } }),
  );
  await page.route("**/api/v1/accounts", (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/api/v1/translations/en", (route) =>
    route.fulfill({
      json: {
        locale: "en",
        defaultLocale: "en",
        revision: 7,
        messages: { inbox: "Inbox from catalog" },
        missingKeys: [],
      },
    }),
  );
  await page.route("**/api/v1/admin/sentry/telemetry", (route) =>
    route.fulfill({
      json: {
        traces: 12,
        spans: 38,
        profiles: 4,
        replays: 0,
        replaySegments: 0,
        replayEnabled: false,
      },
    }),
  );
  await page.route("**/api/v1/admin/status", (route) =>
    route.fulfill({
      json: {
        state: "healthy",
        version: "0.1.0",
        goVersion: "go1.26",
        checkedAt: "2026-09-07T12:00:00Z",
        components: [
          {
            name: "api",
            status: "healthy",
            detail: "available",
            checkedAt: "2026-09-07T12:00:00Z",
          },
        ],
        queue: { ready: 0, pending: 0, retry: 0, dead: 0 },
      },
    }),
  );
  await page.route("**/api/v1/admin/queue", (route) =>
    route.fulfill({
      json: { stats: { ready: 0, pending: 0, retry: 0, dead: 0 }, operations: [] },
    }),
  );
  await page.route("**/api/v1/admin/metrics?**", (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/api/v1/admin/logs?**", (route) =>
    route.fulfill({ json: { items: [], dropped: 0 } }),
  );
  await page.route("**/api/v1/admin/logs/debug", (route) =>
    route.fulfill({ json: { enabled: false, enabledUntil: null } }),
  );
  await page.route("**/api/v1/admin/sentry?**", (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/api/v1/admin/cdn", (route) =>
    route.fulfill({
      json: {
        attachmentObjects: 4,
        attachmentBytes: 2048,
        sentryObjects: 2,
        sentryBytes: 1024,
        missingObjects: 0,
      },
    }),
  );
  await page.route("**/api/v1/admin/translations", (route) =>
    route.fulfill({
      json: {
        defaultLocale: "en",
        revision: 7,
        catalogs: {
          en: { inbox: { value: "Inbox", sourceHash: "hash" } },
          es: { inbox: { value: "Recibidos", sourceHash: "hash" } },
        },
        diagnostics: {
          missingEnglish: [],
          missingSpanish: ["optional.key"],
          staleSpanish: [],
          invalidIcu: [],
          unknownKeys: [],
          privateValues: [],
        },
      },
    }),
  );
  const catalogLoaded = page.waitForResponse((response) =>
    response.url().endsWith("/api/v1/translations/en"),
  );
  await page.goto("/");
  await catalogLoaded;
  await expect(page.getByRole("banner")).toBeVisible();
  await expect(
    page.getByRole("navigation", { name: "Mailboxes", includeHidden: true }),
  ).toHaveCount(1);
  await expect(page.getByRole("main")).toBeVisible();
  const catalogInbox = page.getByRole("button", { name: "Inbox from catalog", exact: true });
  if (!(await catalogInbox.isVisible())) {
    await page.getByRole("button", { name: "Toggle navigation" }).click();
  }
  await expect(catalogInbox).toBeVisible();

  if ((page.viewportSize()?.width ?? 0) >= 1024) {
    await page.getByRole("button", { name: "Settings" }).click();
  } else {
    await page.goto("/settings/accounts");
  }
  await expect(page).toHaveURL(/\/settings\/accounts$/);
  await expect(page.getByRole("heading", { name: "Connected accounts" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Connect Google" })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Connect Microsoft" })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Connect iCloud" })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Connect IMAP" })).toBeEnabled();
  await expect(page.getByText("No mail accounts connected yet.")).toBeVisible();
  await page.getByRole("button", { name: "Connect iCloud" }).click();
  await expect(page.getByRole("heading", { name: "iCloud Mail account" })).toBeVisible();
  await expect(page.getByLabel("App-specific password")).toBeVisible();
  await expect(page.getByLabel("Password", { exact: true })).toHaveCount(0);
  await expect(page.getByText(/never asks for your primary Apple Account password/i)).toBeVisible();
  await expect(page.getByText(/imap\.mail\.me\.com:993/)).toBeVisible();
  await expect(page.getByLabel("Server host")).toHaveCount(0);
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Connect IMAP" }).click();
  await expect(page.getByRole("heading", { name: "IMAP and SMTP account" })).toBeVisible();
  await page.getByRole("button", { name: "Cancel" }).click();

  const accountResults = await new AxeBuilder({ page }).analyze();
  expect(
    accountResults.violations.filter(
      (violation) => violation.impact === "critical" || violation.impact === "serious",
    ),
  ).toEqual([]);

  await page.goto("/admin");
  await expect(page).toHaveURL(/\/admin$/);
  await expect(page.getByRole("heading", { name: "Overview", exact: true })).toBeVisible();
  await expect(page.getByText("Available", { exact: true })).toBeVisible();
  await expect(page.getByText("0.1.0")).toBeVisible();
  if ((page.viewportSize()?.width ?? 0) > 900) {
    await page.getByRole("button", { name: "Translations", exact: true }).click();
  } else {
    await page.locator(".admin-mobile-navigation select").selectOption("translations");
  }
  await expect(page).toHaveURL(/\/admin\/translations$/);
  await expect(page.getByRole("heading", { name: "Translations", exact: true })).toBeVisible();
  await expect(page.getByRole("option", { name: "inbox" })).toBeAttached();
  if ((page.viewportSize()?.width ?? 0) > 900) {
    await page.getByRole("button", { name: "Logs", exact: true }).click();
  } else {
    await page.locator(".admin-mobile-navigation select").selectOption("logs");
  }
  await page.getByRole("button", { name: "Enable for 15 minutes" }).click();
  const confirmation = page.getByRole("alertdialog", { name: "Change debug logging?" });
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole("button", { name: "Cancel" }).click();
  await expect(confirmation).toBeHidden();
  if ((page.viewportSize()?.width ?? 0) > 900) {
    await page.getByRole("button", { name: "Updates", exact: true }).click();
  } else {
    await page.locator(".admin-mobile-navigation select").selectOption("updates");
  }
  await expect(page).toHaveURL(/\/admin\/updates$/);
  await expect(page.getByRole("heading", { name: "Updates", exact: true })).toBeVisible();
  await expect(
    page.getByText(/controls are available only inside the installed macOS application/i),
  ).toBeVisible();
  await expect(page.getByText(/never receive Docker socket access/i)).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).color))
    .toBe("rgb(237, 237, 237)");

  const results = await new AxeBuilder({ page }).analyze();
  const blocking = results.violations.filter(
    (violation) => violation.impact === "critical" || violation.impact === "serious",
  );
  expect(blocking).toEqual([]);
});
