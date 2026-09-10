import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const digest = "a".repeat(64);
const environment = {
  ...process.env,
  MAILFLOW_TRAEFIK_IMAGE: `traefik@sha256:${digest}`,
  MAILFLOW_WEB_IMAGE: `ghcr.io/tutitoos/mailflow-web@sha256:${digest}`,
  MAILFLOW_AUTH_IMAGE: `ghcr.io/tutitoos/mailflow-auth@sha256:${digest}`,
  MAILFLOW_API_IMAGE: `ghcr.io/tutitoos/mailflow-api@sha256:${digest}`,
  MAILFLOW_WORKER_IMAGE: `ghcr.io/tutitoos/mailflow-worker@sha256:${digest}`,
  MAILFLOW_BACKUP_IMAGE: `ghcr.io/tutitoos/mailflow-backup@sha256:${digest}`,
  MAILFLOW_POSTGRES_IMAGE: `postgres@sha256:${digest}`,
  MAILFLOW_REDIS_IMAGE: `redis@sha256:${digest}`,
  MAILFLOW_TRAEFIK_CONFIG_PATH: resolve(root, "deploy/traefik-dynamic.yml.template"),
};

type Service = {
  build?: unknown;
  image: string;
  networks: Record<string, unknown>;
  ports?: Array<{ published: string; target: number }>;
  read_only?: boolean;
  cap_drop?: string[];
  cap_add?: string[];
  group_add?: string[];
  security_opt?: string[];
  mem_limit?: string;
  pids_limit?: number;
  cpus?: number;
  volumes?: Array<{ source: string; target: string }>;
};

function productionConfig(): {
  configs: Record<string, { file: string }>;
  networks: Record<string, { internal?: boolean }>;
  services: Record<string, Service>;
} {
  const result = Bun.spawnSync(
    [
      "docker",
      "compose",
      "--env-file",
      "deploy/.env.production.example",
      "-f",
      "deploy/compose.yml",
      "-f",
      "deploy/compose.production.yml",
      "--profile",
      "backup",
      "config",
      "--format",
      "json",
    ],
    { cwd: root, env: environment },
  );
  expect(result.exitCode, result.stderr.toString()).toBe(0);
  return JSON.parse(result.stdout.toString());
}

describe("production Compose policy", () => {
  const config = productionConfig();

  test("uses only immutable images and disables local builds", () => {
    for (const service of Object.values(config.services)) {
      expect(service.image).toMatch(/@sha256:[a-f0-9]{64}$/);
      expect(service.build).toBeUndefined();
    }
  });

  test("publishes only Traefik on HTTP and HTTPS", () => {
    for (const [name, service] of Object.entries(config.services)) {
      if (name === "traefik") {
        expect(service.ports?.map(({ published, target }) => [published, target])).toEqual([
          ["80", 8080],
          ["443", 8443],
        ]);
      } else {
        expect(service.ports).toBeUndefined();
      }
    }
  });

  test("isolates data services on an internal network", () => {
    expect(config.networks.backend?.internal).toBe(true);
    expect(Object.keys(config.services.postgres?.networks ?? {})).toEqual(["backend"]);
    expect(Object.keys(config.services.redis?.networks ?? {})).toEqual(["backend"]);
    expect(Object.keys(config.services.worker?.networks ?? {})).toEqual(["backend"]);
    expect(Object.keys(config.services.traefik?.networks ?? {})).toEqual(["edge"]);
  });

  test("removes Docker API access and defines edge controls", () => {
    const mounts = config.services.traefik?.volumes ?? [];
    expect(mounts.some(({ source }) => source === "/var/run/docker.sock")).toBe(false);
    expect(config.configs.traefik_dynamic?.file).toBe(
      resolve(root, "deploy/traefik-dynamic.yml.template"),
    );
    const dynamic = readFileSync(resolve(root, "deploy/traefik-dynamic.yml.template"), "utf8");
    expect(dynamic).toContain("stsSeconds: 31536000");
    expect(dynamic).toContain("api-rate-limit:");
    expect(dynamic).toContain("web-rate-limit:");
    expect(dynamic).toContain("PathPrefix(`/api/auth`)");
  });

  test("applies bounded, read-only service defaults", () => {
    for (const [name, service] of Object.entries(config.services)) {
      expect(service.read_only, name).toBe(true);
      expect(service.security_opt, name).toContain("no-new-privileges:true");
      expect(Number(service.mem_limit), name).toBeGreaterThan(0);
      expect(service.pids_limit, name).toBeGreaterThan(0);
      expect(service.cpus, name).toBeGreaterThan(0);
      if (name !== "postgres") expect(service.cap_drop, name).toContain("ALL");
    }
    expect(config.services.auth?.cap_add).toEqual(expect.arrayContaining(["SETGID", "SETUID"]));
    expect(config.services.backup?.group_add).toEqual(["65532"]);
    expect(config.services.backup?.volumes).toContainEqual(
      expect.objectContaining({ target: "/data/cdn", read_only: true }),
    );
  });
});
