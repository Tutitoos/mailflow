import { afterEach, describe, expect, test } from "bun:test";
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const script = resolve(import.meta.dir, "production-compose.sh");
const roots: string[] = [];
const digest = "b".repeat(64);
const requiredSecrets = [
  "better_auth_secret",
  "bootstrap_token",
  "recovery_code",
  "postgres_password",
  "restic_password",
  "master_key",
];
const optionalSecrets = [
  "google_oauth_client_secret",
  "microsoft_oauth_client_secret",
  "alert_smtp_password",
];

afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { force: true, recursive: true });
});

function fixture(): { config: string; envFile: string; secrets: string } {
  const root = mkdtempSync(join(tmpdir(), "mailflow-production-compose-"));
  roots.push(root);
  const secrets = join(root, "secrets");
  const runtime = join(root, "runtime");
  mkdirSync(secrets, { mode: 0o700 });
  mkdirSync(runtime, { mode: 0o700 });
  for (const name of requiredSecrets)
    writeFileSync(join(secrets, name), "fixture\n", { mode: 0o600 });
  for (const name of optionalSecrets) writeFileSync(join(secrets, name), "", { mode: 0o600 });
  const envFile = join(root, "release.env");
  const config = join(runtime, "traefik-dynamic.yml");
  writeFileSync(
    envFile,
    [
      "MAILFLOW_DOMAIN=mail.example.com",
      "ACME_EMAIL=admin@example.com",
      `MAILFLOW_TRAEFIK_IMAGE=traefik@sha256:${digest}`,
      `MAILFLOW_WEB_IMAGE=ghcr.io/tutitoos/mailflow-web@sha256:${digest}`,
      `MAILFLOW_AUTH_IMAGE=ghcr.io/tutitoos/mailflow-auth@sha256:${digest}`,
      `MAILFLOW_API_IMAGE=ghcr.io/tutitoos/mailflow-api@sha256:${digest}`,
      `MAILFLOW_WORKER_IMAGE=ghcr.io/tutitoos/mailflow-worker@sha256:${digest}`,
      `MAILFLOW_BACKUP_IMAGE=ghcr.io/tutitoos/mailflow-backup@sha256:${digest}`,
      `MAILFLOW_POSTGRES_IMAGE=postgres@sha256:${digest}`,
      `MAILFLOW_REDIS_IMAGE=redis@sha256:${digest}`,
      `MAILFLOW_SECRETS_PATH=${secrets}`,
      `MAILFLOW_TRAEFIK_CONFIG_PATH=${config}`,
      "BACKUP_PATH=/srv/mailflow/backups",
      "",
    ].join("\n"),
    { mode: 0o600 },
  );
  return { config, envFile, secrets };
}

describe("production Compose operator guard", () => {
  test("accepts private files and immutable image references", () => {
    const { config, envFile } = fixture();
    const result = Bun.spawnSync(["bash", script, "check", envFile], {
      env: { ...process.env, MAILFLOW_WEB_IMAGE: "private.invalid/ambient:latest" },
    });
    expect(result.exitCode, result.stderr.toString()).toBe(0);
    expect(readFileSync(config, "utf8")).toContain("Host(`mail.example.com`)");
    expect(readFileSync(config, "utf8")).not.toContain("__MAILFLOW_DOMAIN__");
  });

  test("rejects mutable images without echoing their value", () => {
    const { envFile } = fixture();
    writeFileSync(
      envFile,
      readFileSync(envFile, "utf8").replace(
        `ghcr.io/tutitoos/mailflow-web@sha256:${digest}`,
        "private.invalid/mailflow-web:latest",
      ),
      { mode: 0o600 },
    );
    const result = Bun.spawnSync(["bash", script, "check", envFile]);
    expect(result.exitCode).not.toBe(0);
    expect(result.stderr.toString()).toContain("MAILFLOW_WEB_IMAGE must be an immutable");
    expect(result.stderr.toString()).not.toContain("private.invalid");
  });

  test("rejects symlinked secret files", () => {
    const { envFile, secrets } = fixture();
    const optional = join(secrets, "alert_smtp_password");
    rmSync(optional);
    symlinkSync(join(secrets, "postgres_password"), optional);
    chmodSync(secrets, 0o700);
    const result = Bun.spawnSync(["bash", script, "check", envFile]);
    expect(result.exitCode).not.toBe(0);
    expect(result.stderr.toString()).toContain("must be a regular file");
  });
});
