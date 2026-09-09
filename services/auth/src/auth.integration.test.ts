import { afterAll, expect, test } from "bun:test";
import type { Pool } from "pg";
import { createAuthRequestHandler } from "./http";

const databaseUrl = process.env.DATABASE_URL;
let openPool: Pool | undefined;

afterAll(async () => {
  await openPool?.end();
});

const databaseTest = databaseUrl ? test : test.skip;

databaseTest("Better Auth creates one canonical Mailflow profile", async () => {
  process.env.BETTER_AUTH_URL = "http://localhost:3001";
  process.env.BETTER_AUTH_SECRET = "integration-secret-0123456789abcdef";
  process.env.MAILFLOW_BOOTSTRAP_TOKEN = "integration-bootstrap-token";
  process.env.MAILFLOW_RECOVERY_CODE = "integration-recovery-code";

  const { auth, pool } = await import("./auth");
  openPool = pool;
  await pool.query("truncate table users cascade");
  const handleRequest = createAuthRequestHandler(auth, pool, {
    recoveryCode: "integration-recovery-code",
  });

  const nativeCookieBoundary = createAuthRequestHandler(
    {
      handler: async () => {
        const headers = new Headers({ "set-auth-token": "signed.session" });
        headers.append("set-cookie", "better-auth.session_token=signed.session; HttpOnly");
        headers.append("set-cookie", "better-auth.passkey=challenge; HttpOnly");
        return new Response(null, { headers });
      },
    },
    pool,
  );
  const boundedCookies = await nativeCookieBoundary(
    new Request("http://localhost:3001/api/auth/native-cookie-test", {
      headers: { "x-mailflow-native": "1" },
    }),
  );
  expect(boundedCookies.headers.getSetCookie()).toEqual([
    "better-auth.passkey=challenge; HttpOnly",
  ]);
  expect(boundedCookies.headers.get("set-auth-token")).toBe("signed.session");

  const setupBefore = await handleRequest(
    new Request("http://localhost:3001/api/auth/setup/status"),
  );
  expect(setupBefore.headers.get("cache-control")).toBe("no-store");
  expect(await setupBefore.json()).toEqual({ configured: false });

  const request = (email: string) =>
    new Request("http://localhost:3001/api/auth/sign-up/email", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-mailflow-bootstrap-token": "integration-bootstrap-token",
      },
      body: JSON.stringify({
        name: "Mailflow Owner",
        email,
        locale: "es",
        password: "mailflow-test-password",
      }),
    });

  const response = await handleRequest(request("owner@example.test"));
  expect(response.status).toBe(200);

  const profiles = await pool.query<{
    id: string;
    email: string;
    name: string;
    locale: string;
  }>("select id, email, name, locale from users");
  expect(profiles.rows).toHaveLength(1);
  expect(profiles.rows[0]).toMatchObject({
    email: "owner@example.test",
    name: "Mailflow Owner",
    locale: "es",
  });

  const accounts = await pool.query<{ user_id: string }>("select user_id from auth_accounts");
  expect(accounts.rows).toHaveLength(1);
  expect(accounts.rows[0]?.user_id).toBe(profiles.rows[0]?.id);

  const setupAfter = await handleRequest(
    new Request("http://localhost:3001/api/auth/setup/status"),
  );
  expect(await setupAfter.json()).toEqual({ configured: true });

  const secondResponse = await handleRequest(request("second@example.test"));
  expect(secondResponse.status).toBe(403);

  const signInResponse = await handleRequest(
    new Request("http://localhost:3001/api/auth/sign-in/email", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        email: "owner@example.test",
        password: "mailflow-test-password",
      }),
    }),
  );
  expect(signInResponse.status).toBe(200);

  const sessionCookie = signInResponse.headers.get("set-cookie")?.split(";", 1)[0];
  expect(sessionCookie).toBeTruthy();
  const tokenResponse = await handleRequest(
    new Request("http://localhost:3001/api/auth/token", {
      headers: { cookie: sessionCookie ?? "" },
    }),
  );
  expect(tokenResponse.status).toBe(200);
  const { token } = (await tokenResponse.json()) as { token: string };
  const encodedPayload = token.split(".")[1];
  expect(encodedPayload).toBeTruthy();
  const payload = JSON.parse(Buffer.from(encodedPayload ?? "", "base64url").toString("utf8")) as {
    aud: string;
    exp: number;
    iss: string;
    sub: string;
  };
  expect(payload).toMatchObject({
    aud: "mailflow-api",
    iss: "http://localhost:3001",
    sub: profiles.rows[0]?.id,
  });
  expect(payload.exp).toBeGreaterThan(Math.floor(Date.now() / 1000));

  const nativeSignIn = await handleRequest(
    new Request("http://localhost:3001/api/auth/sign-in/email", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-mailflow-native": "1",
        "x-mailflow-installation": "a".repeat(64),
      },
      body: JSON.stringify({
        email: "owner@example.test",
        password: "mailflow-test-password",
      }),
    }),
  );
  expect(nativeSignIn.status).toBe(200);
  expect(nativeSignIn.headers.get("set-cookie")).toBeNull();
  const nativeSession = nativeSignIn.headers.get("set-auth-token");
  expect(nativeSession).toBeTruthy();
  const nativeUserAgent = await pool.query<{ user_agent: string }>(
    "select user_agent from auth_sessions where user_agent like 'MailflowDesktop/%'",
  );
  expect(nativeUserAgent.rows[0]?.user_agent).toBe(`MailflowDesktop/${"a".repeat(64)}`);
  const nativeTokenResponse = await handleRequest(
    new Request("http://localhost:3001/api/auth/token", {
      headers: { authorization: `Bearer ${nativeSession}` },
    }),
  );
  expect(nativeTokenResponse.status).toBe(200);
  await pool.query(
    "insert into auth_passkeys (name, public_key, user_id, credential_id, counter, device_type, backed_up) values ('test passkey', 'sanitized-public-key', $1, 'sanitized-credential', 0, 'singleDevice', false)",
    [profiles.rows[0]?.id],
  );

  const rejectedRecovery = await handleRequest(
    new Request("http://localhost:3001/api/auth/recover", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        recoveryCode: "incorrect-recovery-code",
        newPassword: "replacement-test-password",
      }),
    }),
  );
  expect(rejectedRecovery.status).toBe(403);

  const oversizedRecovery = await handleRequest(
    new Request("http://localhost:3001/api/auth/recover", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ recoveryCode: "x".repeat(1100), newPassword: "valid-test-password" }),
    }),
  );
  expect(oversizedRecovery.status).toBe(413);

  const rejectedOrigin = await handleRequest(
    new Request("http://localhost:3001/api/auth/recover", {
      method: "POST",
      headers: { "content-type": "application/json", origin: "https://untrusted.example.test" },
      body: JSON.stringify({
        recoveryCode: "integration-recovery-code",
        newPassword: "valid-test-password",
      }),
    }),
  );
  expect(rejectedOrigin.status).toBe(403);

  const recovery = await handleRequest(
    new Request("http://localhost:3001/api/auth/recover", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        recoveryCode: "integration-recovery-code",
        newPassword: "replacement-test-password",
      }),
    }),
  );
  expect(recovery.status).toBe(200);
  expect(recovery.headers.get("clear-site-data")).toContain("cookies");
  const passkeysAfterRecovery = await pool.query("select id from auth_passkeys");
  expect(passkeysAfterRecovery.rows).toHaveLength(0);

  const revokedNativeToken = await handleRequest(
    new Request("http://localhost:3001/api/auth/token", {
      headers: { authorization: `Bearer ${nativeSession}` },
    }),
  );
  expect(revokedNativeToken.status).toBe(401);

  const oldPassword = await handleRequest(
    new Request("http://localhost:3001/api/auth/sign-in/email", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        email: "owner@example.test",
        password: "mailflow-test-password",
      }),
    }),
  );
  expect(oldPassword.status).toBe(401);
  const newPassword = await handleRequest(
    new Request("http://localhost:3001/api/auth/sign-in/email", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        email: "owner@example.test",
        password: "replacement-test-password",
      }),
    }),
  );
  expect(newPassword.status).toBe(200);
});
