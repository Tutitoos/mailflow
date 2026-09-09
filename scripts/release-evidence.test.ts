import { describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import type { ContainerReleaseManifest } from "./container-release-manifest";
import {
  createContainerEvidence,
  createFileEvidence,
  verifyReleaseEvidence,
} from "./release-evidence";

const release = "v1.2.3";
const commit = "b".repeat(40);

function fixture(): string {
  const root = mkdtempSync(resolve(tmpdir(), "mailflow-evidence-"));
  writeFileSync(
    resolve(root, "mailflow.cdx.json"),
    '{"bomFormat":"CycloneDX","specVersion":"1.6","version":1}\n',
  );
  writeFileSync(resolve(root, "Mailflow.dmg"), "release artifact\n");
  return root;
}

describe("release evidence", () => {
  test("binds file subjects to exact bytes and rejects tampering", () => {
    const root = fixture();
    const evidence = createFileEvidence(release, commit, root, "mailflow.cdx.json", [
      "Mailflow.dmg",
    ]);
    verifyReleaseEvidence(evidence, root);
    writeFileSync(resolve(root, "Mailflow.dmg"), "tampered artifact\n");
    expect(() => verifyReleaseEvidence(evidence, root)).toThrow("artifact checksum mismatch");
  });

  test("binds immutable OCI subjects to downloaded SBOMs", () => {
    const root = fixture();
    const sbom = readFileSync(resolve(root, "mailflow.cdx.json"));
    for (const name of ["api", "auth", "backup", "web", "worker"]) {
      writeFileSync(resolve(root, `${name}.cdx.json`), sbom);
    }
    const digest = `sha256:${"a".repeat(64)}`;
    const manifest: ContainerReleaseManifest = {
      schemaVersion: 1,
      release,
      commit,
      images: Object.fromEntries(
        ["api", "auth", "backup", "web", "worker"].map((name) => [
          name,
          { digest, reference: `ghcr.io/tutitoos/mailflow-${name}@${digest}` },
        ]),
      ),
    };
    const evidence = createContainerEvidence(release, commit, manifest, root);
    verifyReleaseEvidence(evidence, root);
    writeFileSync(
      resolve(root, "api.cdx.json"),
      '{"bomFormat":"CycloneDX","specVersion":"1.6","version":2}\n',
    );
    expect(() => verifyReleaseEvidence(evidence, root)).toThrow("SBOM checksum mismatch");
  });

  test("rejects unsafe names, duplicate subjects, and mismatched identities", () => {
    const root = fixture();
    expect(() =>
      createFileEvidence(release, commit, root, "mailflow.cdx.json", ["../Mailflow.dmg"]),
    ).toThrow("unsafe evidence filename");
    expect(() =>
      createFileEvidence(release, commit, root, "mailflow.cdx.json", [
        "Mailflow.dmg",
        "Mailflow.dmg",
      ]),
    ).toThrow("duplicate subject");
    symlinkSync(resolve(root, "Mailflow.dmg"), resolve(root, "linked.dmg"));
    expect(() =>
      createFileEvidence(release, commit, root, "mailflow.cdx.json", ["linked.dmg"]),
    ).toThrow("not a regular file");
    expect(() =>
      createContainerEvidence(
        release,
        commit,
        { schemaVersion: 1, release: "v1.2.4", commit, images: {} },
        root,
      ),
    ).toThrow("identity does not match");
  });

  test("rejects malformed CycloneDX metadata", () => {
    const root = fixture();
    writeFileSync(resolve(root, "mailflow.cdx.json"), "{}\n");
    expect(() =>
      createFileEvidence(release, commit, root, "mailflow.cdx.json", ["Mailflow.dmg"]),
    ).toThrow("invalid CycloneDX SBOM");
  });
});
