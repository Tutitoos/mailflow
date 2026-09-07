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
  user: {
    modelName: "users",
    fields: {
      emailVerified: "email_verified",
      createdAt: "created_at",
      updatedAt: "updated_at",
    },
    additionalFields: {
      locale: {
        type: "string",
        required: false,
        defaultValue: "en",
        fieldName: "locale",
      },
    },
  },
  session: {
    modelName: "auth_sessions",
    fields: {
      expiresAt: "expires_at",
      createdAt: "created_at",
      updatedAt: "updated_at",
      ipAddress: "ip_address",
      userAgent: "user_agent",
      userId: "user_id",
    },
  },
  account: {
    modelName: "auth_accounts",
    fields: {
      accountId: "account_id",
      providerId: "provider_id",
      userId: "user_id",
      accessToken: "access_token",
      refreshToken: "refresh_token",
      idToken: "id_token",
      accessTokenExpiresAt: "access_token_expires_at",
      refreshTokenExpiresAt: "refresh_token_expires_at",
      createdAt: "created_at",
      updatedAt: "updated_at",
    },
  },
  verification: {
    modelName: "auth_verifications",
    fields: {
      expiresAt: "expires_at",
      createdAt: "created_at",
      updatedAt: "updated_at",
    },
  },
  plugins: [
    passkey({
      schema: {
        passkey: {
          modelName: "auth_passkeys",
          fields: {
            publicKey: "public_key",
            userId: "user_id",
            credentialID: "credential_id",
            deviceType: "device_type",
            backedUp: "backed_up",
            createdAt: "created_at",
          },
        },
      },
    }),
    jwt({
      jwks: { rotationInterval: 60 * 60 * 24 * 30, gracePeriod: 60 * 60 * 24 * 30 },
      jwt: {
        audience: config.audience,
        expirationTime: "15m",
        issuer: new URL(config.baseUrl).origin,
      },
      schema: {
        jwks: {
          modelName: "auth_jwks",
          fields: {
            publicKey: "public_key",
            privateKey: "private_key",
            createdAt: "created_at",
            expiresAt: "expires_at",
          },
        },
      },
    }),
  ],
  databaseHooks: {
    user: {
      create: {
        before: async (user, context) => {
          const result = await pool.query<{ count: string }>(
            "select count(*)::text as count from users",
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
    database: { generateId: "uuid", joins: true },
    useSecureCookies: config.baseUrl.startsWith("https://"),
  },
});
