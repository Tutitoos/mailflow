import { timingSafeEqual } from "node:crypto";
import { hashPassword } from "better-auth/crypto";
import type { Pool } from "pg";
import type { AuthConfig } from "./config";

type AuthHandler = {
  handler(request: Request): Promise<Response>;
};

const RECOVERY_WINDOW_MS = 15 * 60 * 1000;
const RECOVERY_MAX_ATTEMPTS = 5;

function secretsMatch(expected: string, received: unknown): boolean {
  if (typeof received !== "string") return false;
  const expectedBytes = Buffer.from(expected);
  const receivedBytes = Buffer.from(received);
  return (
    expectedBytes.length === receivedBytes.length && timingSafeEqual(expectedBytes, receivedBytes)
  );
}

function isNativeRequest(request: Request): boolean {
  return request.headers.get("x-mailflow-native") === "1";
}

function identifyNativeRequest(request: Request): Request {
  const installation = request.headers.get("x-mailflow-installation") ?? "";
  if (!/^[a-f0-9]{64}$/.test(installation)) return request;
  const headers = new Headers(request.headers);
  headers.set("user-agent", `MailflowDesktop/${installation}`);
  return new Request(request, { headers });
}

function withoutNativeSessionCookies(response: Response): Response {
  const headers = new Headers(response.headers);
  const getSetCookie = (response.headers as Headers & { getSetCookie?: () => string[] })
    .getSetCookie;
  const cookies = getSetCookie ? getSetCookie.call(response.headers) : [];
  headers.delete("set-cookie");
  for (const cookie of cookies) {
    const name = cookie.slice(0, cookie.indexOf("=")).trim().toLowerCase();
    if (!name.includes("better-auth.session_")) headers.append("set-cookie", cookie);
  }
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

export function createAuthRequestHandler(
  auth: AuthHandler,
  pool: Pool,
  options?: Pick<AuthConfig, "recoveryCode"> & Partial<Pick<AuthConfig, "trustedOrigins">>,
) {
  let recoveryWindowStartedAt = 0;
  let recoveryAttempts = 0;

  return async (request: Request): Promise<Response> => {
    const url = new URL(request.url);
    if (url.pathname === "/health/live") {
      return Response.json({ status: "ok", service: "auth" });
    }
    if (url.pathname === "/health/ready") {
      try {
        await pool.query("select 1");
        return Response.json({ status: "ready", service: "auth" });
      } catch {
        return Response.json({ status: "unavailable", service: "auth" }, { status: 503 });
      }
    }
    if (url.pathname === "/api/auth/setup/status") {
      if (request.method !== "GET") {
        return Response.json(
          { code: "method_not_allowed", detail: "Method not allowed" },
          { status: 405, headers: { Allow: "GET", "Cache-Control": "no-store" } },
        );
      }
      try {
        const result = await pool.query<{ configured: boolean }>(
          "select exists(select 1 from users) as configured",
        );
        return Response.json(
          { configured: result.rows[0]?.configured ?? false },
          { headers: { "Cache-Control": "no-store" } },
        );
      } catch {
        return Response.json(
          { code: "setup_status_unavailable", detail: "Setup status is unavailable" },
          { status: 503, headers: { "Cache-Control": "no-store" } },
        );
      }
    }
    if (url.pathname === "/api/auth/recover") {
      if (request.method !== "POST") {
        return Response.json(
          { code: "method_not_allowed", detail: "Method not allowed" },
          { status: 405, headers: { Allow: "POST", "Cache-Control": "no-store" } },
        );
      }
      const origin = request.headers.get("origin");
      if (origin && !options?.trustedOrigins?.includes(origin)) {
        return Response.json(
          { code: "origin_rejected", detail: "Origin is not allowed" },
          { status: 403, headers: { "Cache-Control": "no-store" } },
        );
      }
      const now = Date.now();
      if (now - recoveryWindowStartedAt >= RECOVERY_WINDOW_MS) {
        recoveryWindowStartedAt = now;
        recoveryAttempts = 0;
      }
      if (recoveryAttempts >= RECOVERY_MAX_ATTEMPTS) {
        return Response.json(
          { code: "recovery_throttled", detail: "Recovery is temporarily unavailable" },
          { status: 429, headers: { "Cache-Control": "no-store", "Retry-After": "900" } },
        );
      }
      let body: { recoveryCode?: unknown; newPassword?: unknown };
      const contentLength = Number.parseInt(request.headers.get("content-length") ?? "0", 10);
      if (Number.isFinite(contentLength) && contentLength > 1024) {
        return Response.json(
          { code: "invalid_recovery", detail: "Recovery request is invalid" },
          { status: 413, headers: { "Cache-Control": "no-store" } },
        );
      }
      let encoded: string;
      try {
        encoded = await request.text();
      } catch {
        return Response.json(
          { code: "invalid_recovery", detail: "Recovery request is invalid" },
          { status: 400, headers: { "Cache-Control": "no-store" } },
        );
      }
      if (encoded.length > 1024) {
        return Response.json(
          { code: "invalid_recovery", detail: "Recovery request is invalid" },
          { status: 413, headers: { "Cache-Control": "no-store" } },
        );
      }
      try {
        body = JSON.parse(encoded) as typeof body;
      } catch {
        return Response.json(
          { code: "invalid_recovery", detail: "Recovery request is invalid" },
          { status: 400, headers: { "Cache-Control": "no-store" } },
        );
      }
      const newPassword = body.newPassword;
      const recoveryCode = options?.recoveryCode;
      if (
        typeof newPassword !== "string" ||
        newPassword.length < 12 ||
        newPassword.length > 128 ||
        !recoveryCode ||
        typeof body.recoveryCode !== "string" ||
        body.recoveryCode.length > 256 ||
        !secretsMatch(recoveryCode, body.recoveryCode)
      ) {
        recoveryAttempts += 1;
        return Response.json(
          { code: "invalid_recovery", detail: "Recovery request is invalid" },
          { status: 403, headers: { "Cache-Control": "no-store" } },
        );
      }
      const client = await pool.connect();
      try {
        await client.query("begin");
        const owner = await client.query<{ id: string }>("select id from users limit 1 for update");
        const ownerId = owner.rows[0]?.id;
        if (!ownerId) {
          await client.query("rollback");
          return Response.json(
            { code: "setup_required", detail: "Mailflow is not configured" },
            { status: 409, headers: { "Cache-Control": "no-store" } },
          );
        }
        const password = await hashPassword(newPassword);
        const updated = await client.query(
          "update auth_accounts set password = $1, updated_at = now() where user_id = $2 and provider_id = 'credential'",
          [password, ownerId],
        );
        if (updated.rowCount !== 1) throw new Error("credential account unavailable");
        await client.query("delete from auth_passkeys where user_id = $1", [ownerId]);
        await client.query("delete from auth_sessions where user_id = $1", [ownerId]);
        await client.query("commit");
        recoveryAttempts = 0;
        return Response.json(
          { recovered: true },
          { headers: { "Cache-Control": "no-store", "Clear-Site-Data": '"cookies"' } },
        );
      } catch {
        await client.query("rollback").catch(() => undefined);
        return Response.json(
          { code: "recovery_unavailable", detail: "Recovery is unavailable" },
          { status: 503, headers: { "Cache-Control": "no-store" } },
        );
      } finally {
        client.release();
      }
    }
    const response = await auth.handler(
      isNativeRequest(request) ? identifyNativeRequest(request) : request,
    );
    return isNativeRequest(request) ? withoutNativeSessionCookies(response) : response;
  };
}
