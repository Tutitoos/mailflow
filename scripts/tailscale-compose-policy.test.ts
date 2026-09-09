import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");

type Service = {
  image: string;
  ports?: Array<{ host_ip: string; published: string; target: number }>;
  volumes?: Array<{ source: string; target: string; read_only?: boolean }>;
};

function tailscaleConfig(): { services: Record<string, Service> } {
  const result = Bun.spawnSync(
    [
      "docker",
      "compose",
      "--env-file",
      "deploy/.env.example",
      "-f",
      "deploy/compose.yml",
      "-f",
      "deploy/compose.tailscale.yml",
      "config",
      "--format",
      "json",
    ],
    { cwd: root },
  );
  expect(result.exitCode, result.stderr.toString()).toBe(0);
  return JSON.parse(result.stdout.toString());
}

describe("private Tailscale Compose policy", () => {
  const config = tailscaleConfig();

  test("keeps the public edge inactive", () => {
    expect(config.services.traefik).toBeUndefined();
    expect(config.services.backup).toBeUndefined();
  });

  test("publishes only the loopback gateway", () => {
    for (const [name, service] of Object.entries(config.services)) {
      if (name === "tailscale-gateway") {
        expect(service.ports).toEqual([
          {
            mode: "ingress",
            host_ip: "127.0.0.1",
            target: 8080,
            published: "8090",
            protocol: "tcp",
          },
        ]);
      } else {
        expect(service.ports, name).toBeUndefined();
      }
    }
  });

  test("mounts a read-only same-origin proxy configuration", () => {
    const gateway = config.services["tailscale-gateway"];
    expect(gateway?.image).toBe("nginx:1.29-alpine");
    expect(gateway?.volumes).toEqual([
      expect.objectContaining({
        source: resolve(root, "deploy/nginx.tailscale.conf"),
        target: "/etc/nginx/conf.d/default.conf",
        read_only: true,
      }),
    ]);

    const nginx = readFileSync(resolve(root, "deploy/nginx.tailscale.conf"), "utf8");
    for (const route of ["/api/auth/", "/api/v1/", "/sentry/", "/"]) {
      expect(nginx).toContain(`location ${route === "/" ? "" : "^~ "}${route}`);
    }
    expect(nginx).toContain("proxy_set_header X-Forwarded-Proto https;");
    expect(nginx).toContain("proxy_set_header Upgrade $http_upgrade;");
  });
});
