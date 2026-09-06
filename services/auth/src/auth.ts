import { timingSafeEqual } from "node:crypto";
import { passkey } from "@better-auth/passkey";
import { betterAuth } from "better-auth";
import { APIError } from "better-auth/api";
import { jwt } from "better-auth/plugins";
import { Pool } from "pg";
import { loadConfig } from "./config";

const config = loadConfig();

export const pool = new Pool({ connectionString: config.databaseUrl });

function tokensMatch(received: string | null): boolean {
  if (!received) return false;
  const expectedBytes = Buffer.from(config.bootstrapToken);
  const receivedBytes = Buffer.from(received);
  return (
    expectedBytes.length === receivedBytes.length && timingSafeEqual(expectedBytes, receivedBytes)
  );
}

export const auth = betterAuth({
  appName: "Mailflow",
  baseURL: config.baseUrl,
  database: pool,
  secret: config.secret,
  trustedOrigins: config.trustedOrigins,
  emailAndPassword: {
    enabled: true,
    minPasswordLength: 12,
  },
  plugins: [
    passkey(),
    jwt({
      jwks: { rotationInterval: 60 * 60 * 24 * 30, gracePeriod: 60 * 60 * 24 * 30 },
      jwt: { expirationTime: "15m" },
    }),
  ],
  databaseHooks: {
    user: {
      create: {
        before: async (user, context) => {
          const result = await pool.query<{ count: string }>(
            'select count(*)::text as count from "user"',
          );
          if (result.rows[0]?.count !== "0") {
            throw new APIError("FORBIDDEN", { message: "Registration is closed" });
          }
          const token = context?.headers?.get("x-mailflow-bootstrap-token") ?? null;
          if (!tokensMatch(token)) {
            throw new APIError("FORBIDDEN", { message: "Invalid bootstrap token" });
          }
          return { data: user };
        },
      },
    },
  },
  advanced: {
    database: { joins: true },
    useSecureCookies: config.baseUrl.startsWith("https://"),
  },
});
