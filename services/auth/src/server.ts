import { auth, pool } from "./auth";
import { loadConfig } from "./config";

const config = loadConfig();

const server = Bun.serve({
  port: config.port,
  async fetch(request) {
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
    return auth.handler(request);
  },
});

console.info(JSON.stringify({ event: "auth.started", port: server.port }));

async function shutdown() {
  server.stop();
  await pool.end();
}

process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);
