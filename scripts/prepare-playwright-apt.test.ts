import { afterEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const script = resolve(import.meta.dir, "prepare-playwright-apt.sh");
const workflow = readFileSync(resolve(import.meta.dir, "../.github/workflows/ci.yml"), "utf8");
const roots: string[] = [];

afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { force: true, recursive: true });
});

function aptRoot(): string {
  const root = mkdtempSync(join(tmpdir(), "mailflow-playwright-apt-"));
  roots.push(root);
  mkdirSync(join(root, "sources.list.d"));
  return root;
}

describe("Playwright APT isolation", () => {
  test("runs before Playwright installs system dependencies", () => {
    const isolation = workflow.indexOf("./scripts/prepare-playwright-apt.sh");
    const installation = workflow.indexOf("playwright install --with-deps chromium");
    expect(isolation).toBeGreaterThan(-1);
    expect(installation).toBeGreaterThan(isolation);
  });

  test("disables only the unrelated Google Chrome source", () => {
    const root = aptRoot();
    const directory = join(root, "sources.list.d");
    const chrome = join(directory, "google-chrome.list");
    const ubuntu = join(directory, "ubuntu.sources");
    writeFileSync(chrome, "deb https://dl.google.com/linux/chrome/deb/ stable main\n");
    writeFileSync(ubuntu, "URIs: http://archive.ubuntu.com/ubuntu\n");

    const result = Bun.spawnSync(["bash", script, root]);
    const repeated = Bun.spawnSync(["bash", script, root]);

    expect(result.exitCode).toBe(0);
    expect(repeated.exitCode).toBe(0);
    expect(readFileSync(`${chrome}.mailflow-disabled`, "utf8")).toContain("dl.google.com");
    expect(readFileSync(ubuntu, "utf8")).toContain("archive.ubuntu.com");
  });

  test("is a no-op when the runner has no Chrome source", () => {
    const root = aptRoot();
    const ubuntu = join(root, "sources.list.d", "ubuntu.sources");
    writeFileSync(ubuntu, "URIs: http://archive.ubuntu.com/ubuntu\n");

    const result = Bun.spawnSync(["bash", script, root]);

    expect(result.exitCode).toBe(0);
    expect(readFileSync(ubuntu, "utf8")).toContain("archive.ubuntu.com");
  });
});
