import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

describe("1.0 product acceptance policy", () => {
  test("keeps the 100,000-message performance gates executable", () => {
    const search = read("services/api/internal/modules/mail/search_repository_test.go");
    const inbox = read("apps/web/tests/inbox.spec.ts");
    expect(search).toContain("generate_series(1, 100000)");
    expect(search).toContain("elapsed >= time.Second");
    expect(search).toContain("messages_search_idx");
    expect(inbox).toContain("100_000");
    expect(inbox).toContain(".message-row");
    expect(inbox).toContain("toBeLessThan(100)");
  });

  test("covers all documented widths, accessibility and reduced motion", () => {
    const config = read("apps/web/playwright.config.ts");
    const inbox = read("apps/web/tests/inbox.spec.ts");
    const shell = read("apps/web/tests/shell.spec.ts");
    for (const project of [
      "desktop-large",
      "desktop",
      "tablet-landscape",
      "tablet-portrait",
      "mobile-large",
      "mobile",
    ]) {
      expect(config).toContain(`"${project}"`);
    }
    expect(config).toContain('reducedMotion: "reduce"');
    expect(inbox).toContain('keyboard.press("/")');
    expect(inbox).toContain('keyboard.press("e")');
    expect(inbox).toContain("AxeBuilder");
    expect(shell).toContain("AxeBuilder");
  });

  test("pins provider timing and durable restart gates", () => {
    const sync = read("services/api/internal/modules/sync/runs.go");
    const imap = read("services/api/internal/modules/imap/idle_test.go");
    const restart = read("scripts/verify-container-images.sh");
    const build = read("scripts/build-acceptance-images.sh");
    const workflow = read(".github/workflows/container-release.yml");
    expect(sync).toContain("ActivePollInterval   = 2 * time.Minute");
    expect(sync).toContain("IdlePollInterval     = 10 * time.Minute");
    expect(imap).toContain("20 * time.Millisecond");
    expect(restart).toContain("provider cursor changed across the PostgreSQL restart");
    expect(restart).toContain("committed action changed across the PostgreSQL restart");
    expect(restart).toContain("Redis restart marker was not durable");
    expect(restart).toContain("restart api worker");
    for (const component of ["web", "auth", "api", "worker", "backup"]) {
      expect(build).toContain(`mailflow-${component}:acceptance`);
    }
    expect(workflow).toContain("./scripts/build-acceptance-images.sh");
    expect(workflow).toContain("./scripts/verify-container-images.sh health");
  });
});
