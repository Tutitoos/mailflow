import type { Pool } from "pg";

type AuthHandler = {
  handler(request: Request): Promise<Response>;
};

export function createAuthRequestHandler(auth: AuthHandler, pool: Pool) {
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
    return auth.handler(request);
  };
}
