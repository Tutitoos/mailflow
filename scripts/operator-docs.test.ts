import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const documents = [
  resolve(root, "docs/operator-runbook.md"),
  resolve(root, "docs/es/operator-runbook.md"),
];

function localLinks(path: string, content: string): string[] {
  return [...content.matchAll(/\[[^\]]+\]\(([^)]+)\)/g)]
    .map((match) => match[1].split("#", 1)[0])
    .filter((target) => target.length > 0 && !target.includes("://"))
    .map((target) => resolve(dirname(path), target));
}

function shellBlocks(content: string): string {
  return [...content.matchAll(/```sh\n([\s\S]*?)```/g)].map((match) => match[1]).join("\n");
}

describe("operator runbooks", () => {
  test("English and Spanish cover the same ordered lifecycle", () => {
    for (const path of documents) {
      const content = readFileSync(path, "utf8");
      for (let step = 1; step <= 9; step += 1) expect(content).toContain(`## ${step}.`);
      expect(content.indexOf("## 1.")).toBeLessThan(content.indexOf("## 9."));
      expect(content).toMatch(/Google/);
      expect(content).toMatch(/Microsoft/);
      expect(content).toMatch(/iCloud/);
      expect(content).toMatch(/IMAP\/SMTP/);
      expect(content).toContain("production-compose.sh check");
      expect(content).toContain("production-compose.sh apply");
      expect(content).toContain("production-compose.sh rollback");
      expect(content).toContain("run --rm backup run");
      expect(content).toContain("down --volumes --remove-orphans");
    }
  });

  test("every local documentation link resolves", () => {
    for (const path of documents) {
      const content = readFileSync(path, "utf8");
      for (const target of localLinks(path, content)) {
        expect(existsSync(target), `missing link target ${target}`).toBe(true);
      }
    }
  });

  test("copyable commands retain safe identities and permissions", () => {
    for (const path of documents) {
      const commands = shellBlocks(readFileSync(path, "utf8"));
      expect(commands).toContain('git checkout --detach "$MAILFLOW_SOURCE_COMMIT"');
      expect(commands).toContain("grep -Eq '^[0-9a-f]{40}$'");
      expect(commands).toContain("chmod 600");
      expect(commands).not.toMatch(/chmod\s+(?:-R\s+)?777/);
      expect(commands).not.toContain(":latest");
      expect(commands).not.toContain("/var/run/docker.sock");
      expect(commands).not.toContain("--privileged");
      expect(commands).not.toMatch(/curl\s+[^\n]*(?:-k|--insecure)/);
      expect(commands).not.toMatch(/rm\s+-rf/);
    }
  });

  test("secret inventory and destructive boundaries stay explicit", () => {
    const required = [
      "better_auth_secret",
      "bootstrap_token",
      "recovery_code",
      "master_key",
      "postgres_password",
      "restic_password",
      "google_oauth_client_secret",
      "microsoft_oauth_client_secret",
      "alert_smtp_password",
    ];
    for (const path of documents) {
      const content = readFileSync(path, "utf8");
      for (const secret of required) expect(content).toContain(secret);
      expect(content).toMatch(/\*\*(?:Destructive|Destructivo):\*\*/);
      expect(content).toContain("mailflow-restore");
      expect(content).toMatch(/project|proyecto/);
    }
  });
});
