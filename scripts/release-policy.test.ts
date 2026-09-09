import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const container = readFileSync(resolve(root, ".github/workflows/container-release.yml"), "utf8");
const desktop = readFileSync(resolve(root, ".github/workflows/desktop-release.yml"), "utf8");

function job(workflow: string, name: string, nextName?: string): string {
  const start = workflow.indexOf(`\n  ${name}:\n`);
  if (start < 0) throw new Error(`missing job: ${name}`);
  const end = nextName ? workflow.indexOf(`\n  ${nextName}:\n`, start + 1) : workflow.length;
  if (end < 0) throw new Error(`missing next job: ${nextName}`);
  return workflow.slice(start, end);
}

describe("release workflow authority", () => {
  test("keeps pull-request container jobs secretless and read-only", () => {
    const contract = job(container, "multi-architecture-contract", "acceptance");
    const acceptance = job(container, "acceptance", "publish");
    for (const pullRequestJob of [contract, acceptance]) {
      expect(pullRequestJob).not.toContain("environment:");
      expect(pullRequestJob).not.toContain("id-token: write");
      expect(pullRequestJob).not.toContain("packages: write");
      expect(pullRequestJob).not.toContain("attestations: write");
      expect(pullRequestJob).not.toContain("artifact-metadata: write");
      expect(pullRequestJob).not.toContain("secrets.");
    }
  });

  test("gates container signing authority on protected tag jobs", () => {
    const publish = job(container, "publish", "finalize");
    const finalize = job(container, "finalize");
    for (const releaseJob of [publish, finalize]) {
      expect(releaseJob).toContain("github.event_name == 'push'");
      expect(releaseJob).toContain("startsWith(github.ref, 'refs/tags/v')");
      expect(releaseJob).toContain("environment: container-release");
      expect(releaseJob).toContain("id-token: write");
    }
    expect(publish).toContain("packages: write");
    expect(publish).toContain("cosign sign --yes");
    expect(publish).toContain("provenance: mode=max");
    expect(publish).toContain("sbom: true");
  });

  test("keeps desktop PR builds separate from protected signing", () => {
    const contract = job(desktop, "universal-contract", "macos-universal");
    const release = job(desktop, "macos-universal");
    expect(contract).not.toContain("environment:");
    expect(contract).not.toContain("id-token: write");
    expect(contract).not.toContain("secrets.");
    expect(release).toContain("github.event_name != 'pull_request'");
    expect(release).toContain("environment: desktop-release");
    expect(release).toContain("id-token: write");
    expect(release).toContain("actions/attest@v4");
    expect(release).toContain("cosign sign-blob --yes");
  });
});
