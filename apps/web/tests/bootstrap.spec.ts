import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

test("creates the only owner in Spanish without persisting secrets", async ({ page }) => {
  const bootstrapToken = "browser-only-bootstrap-token";
  const password = "browser-test-password";
  let configured = false;
  let submittedLocale: string | undefined;

  await page.route("**/api/auth/setup/status", (route) => route.fulfill({ json: { configured } }));
  await page.route("**/api/auth/get-session", (route) => route.fulfill({ json: null }));
  await page.route("**/api/auth/sign-up/email", async (route) => {
    const request = route.request();
    const body = request.postDataJSON() as Record<string, string>;
    expect(request.url()).not.toContain(bootstrapToken);
    expect(body).not.toHaveProperty("bootstrapToken");
    expect(request.headers()["x-mailflow-bootstrap-token"]).toBe(bootstrapToken);
    submittedLocale = body.locale;
    configured = true;
    await route.fulfill({ json: { user: { id: "test-owner" } } });
  });

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Create your Mailflow owner" })).toBeVisible();
  const accessibility = await new AxeBuilder({ page }).analyze();
  expect(
    accessibility.violations.filter(
      (violation) => violation.impact === "critical" || violation.impact === "serious",
    ),
  ).toEqual([]);
  await page.getByRole("button", { name: "Language" }).click();
  const languageMenu = page.getByRole("menu", { name: "Language" });
  await expect(languageMenu).toBeVisible();
  await expect(page.getByRole("menuitemradio", { name: "EN English" })).toHaveAttribute(
    "aria-checked",
    "true",
  );
  await page.getByRole("menuitemradio", { name: "ES Spanish" }).click();
  await expect(
    page.getByRole("heading", { name: "Crea el propietario de Mailflow" }),
  ).toBeVisible();

  await page.getByLabel("Nombre").fill("Mailflow Owner");
  await page.getByLabel("Correo electrónico").fill("owner@example.test");
  await page.getByLabel("Contraseña").fill(password);
  await page.getByLabel("Token de instalación").fill(bootstrapToken);
  await page.getByRole("button", { name: "Crear propietario" }).click();

  await expect(page.getByRole("banner")).toBeVisible();
  expect(submittedLocale).toBe("es");
  expect(page.url()).not.toContain(bootstrapToken);
  expect(await page.evaluate(() => localStorage.length + sessionStorage.length)).toBe(0);
  expect(await page.locator("body").innerText()).not.toContain(bootstrapToken);
  expect(await page.locator("body").innerText()).not.toContain(password);

  await page.reload();
  await expect(page.getByRole("heading", { name: "Sign in to Mailflow" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Create your Mailflow owner" })).toHaveCount(0);
});

test("shows deterministic errors and clears the installation token", async ({ page }) => {
  const bootstrapToken = "rejected-bootstrap-token";
  await page.route("**/api/auth/setup/status", (route) =>
    route.fulfill({ json: { configured: false } }),
  );
  await page.route("**/api/auth/sign-up/email", (route) =>
    route.fulfill({ status: 403, json: { message: "sensitive upstream detail" } }),
  );

  await page.goto("/");
  await page.getByLabel("Name").fill("Mailflow Owner");
  await page.getByLabel("Email").fill("owner@example.test");
  await page.getByLabel("Password").fill("browser-test-password");
  await page.getByLabel("Installation token").fill(bootstrapToken);
  await page.getByRole("button", { name: "Create owner" }).click();

  await expect(page.getByRole("alert")).toHaveText(
    "The installation token is invalid or registration is closed.",
  );
  await expect(page.getByLabel("Installation token")).toHaveValue("");
  await expect(page.getByLabel("Password")).toHaveValue("");
  expect(await page.locator("body").innerText()).not.toContain("sensitive upstream detail");
});

test("offers localized passkey and offline recovery without persisting the code", async ({
  page,
}) => {
  const recoveryCode = "offline-recovery-code";
  const newPassword = "replacement-browser-password";
  await page.route("**/api/auth/setup/status", (route) =>
    route.fulfill({ json: { configured: true } }),
  );
  await page.route("**/api/auth/get-session", (route) => route.fulfill({ json: null }));
  await page.route("**/api/auth/recover", async (route) => {
    const request = route.request();
    expect(request.url()).not.toContain(recoveryCode);
    expect(request.postDataJSON()).toEqual({ recoveryCode, newPassword });
    await route.fulfill({ json: { recovered: true } });
  });

  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sign in with a passkey" })).toBeVisible();
  await page.getByRole("button", { name: "Use recovery code" }).click();
  await expect(page.getByRole("heading", { name: "Recover your Mailflow owner" })).toBeVisible();
  const accessibility = await new AxeBuilder({ page }).analyze();
  expect(
    accessibility.violations.filter(
      (violation) => violation.impact === "critical" || violation.impact === "serious",
    ),
  ).toEqual([]);

  await page.getByLabel("Recovery code").fill(recoveryCode);
  await page.getByLabel("New password").fill(newPassword);
  await page.getByRole("button", { name: "Recover account" }).click();

  await expect(page.getByRole("heading", { name: "Sign in to Mailflow" })).toBeVisible();
  expect(page.url()).not.toContain(recoveryCode);
  expect(await page.locator("body").innerText()).not.toContain(recoveryCode);
  expect(await page.locator("body").innerText()).not.toContain(newPassword);
  expect(await page.evaluate(() => localStorage.length + sessionStorage.length)).toBe(0);
});
