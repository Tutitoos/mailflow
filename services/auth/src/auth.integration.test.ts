import { afterAll, expect, test } from "bun:test";
import type { Pool } from "pg";

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
        password: "mailflow-test-password",
      }),
    });

  const response = await auth.handler(request("owner@example.test"));
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
    locale: "en",
  });

  const accounts = await pool.query<{ user_id: string }>("select user_id from auth_accounts");
  expect(accounts.rows).toHaveLength(1);
  expect(accounts.rows[0]?.user_id).toBe(profiles.rows[0]?.id);

  const secondResponse = await auth.handler(request("second@example.test"));
  expect(secondResponse.status).toBe(403);
});
