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

  const { auth, pool } = await import("./auth");
  openPool = pool;
  await pool.query("truncate table users cascade");
  const handleRequest = createAuthRequestHandler(auth, pool);

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
});
