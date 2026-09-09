import { auth, pool } from "./auth";
import { loadConfig } from "./config";
import { createAuthRequestHandler } from "./http";

const config = loadConfig();
const handleRequest = createAuthRequestHandler(auth, pool, config);

const server = Bun.serve({
  port: config.port,
  maxRequestBodySize: 64 * 1024,
  fetch: handleRequest,
});

console.info(JSON.stringify({ event: "auth.started", port: server.port }));

async function shutdown() {
  server.stop();
  await pool.end();
}

process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);
