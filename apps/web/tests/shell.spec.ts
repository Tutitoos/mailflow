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
  await page.route("**/api/v1/accounts", (route) => route.fulfill({ json: { items: [] } }));
  await page.goto("/");
  await expect(page.getByRole("banner")).toBeVisible();
  await expect(
    page.getByRole("navigation", { name: "Mailboxes", includeHidden: true }),
  ).toHaveCount(1);
  await expect(page.getByRole("main")).toBeVisible();

  if ((page.viewportSize()?.width ?? 0) >= 1024) {
    await page.getByRole("button", { name: "Settings" }).click();
  } else {
    await page.goto("/settings/accounts");
  }
  await expect(page).toHaveURL(/\/settings\/accounts$/);
  await expect(page.getByRole("heading", { name: "Connected accounts" })).toBeVisible();
  await expect(page.getByText("No Google accounts connected yet.")).toBeVisible();

  const accountResults = await new AxeBuilder({ page }).analyze();
  expect(
    accountResults.violations.filter(
      (violation) => violation.impact === "critical" || violation.impact === "serious",
    ),
  ).toEqual([]);

  await page.goto("/admin");
  await expect(page).toHaveURL(/\/admin$/);
  await expect(page.getByRole("heading", { name: "Operational status" })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).color))
    .toBe("rgb(237, 237, 237)");

  const results = await new AxeBuilder({ page }).analyze();
  const blocking = results.violations.filter(
    (violation) => violation.impact === "critical" || violation.impact === "serious",
  );
  expect(blocking).toEqual([]);
});
