import { createHash } from "node:crypto";
import { lstatSync, readFileSync, writeFileSync } from "node:fs";
import { basename, resolve } from "node:path";
import type { ContainerReleaseManifest } from "./container-release-manifest";

const RELEASE = /^v\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)\.\d+)?$/;
const COMMIT = /^[a-f0-9]{40}$/;
const SHA256 = /^[a-f0-9]{64}$/;
const OCI_DIGEST = /^sha256:[a-f0-9]{64}$/;
const EXPECTED_CONTAINERS = ["api", "auth", "backup", "web", "worker"];

type SbomRecord = { name: string; sha256: string };

export type FileSubject = {
  kind: "file";
  name: string;
  sha256: string;
  size: number;
  sbom: SbomRecord;
};

export type OciSubject = {
  kind: "oci";
  name: string;
  reference: string;
  digest: string;
  sbom: SbomRecord;
};

export type ReleaseEvidence = {
  schemaVersion: 1;
  release: string;
  commit: string;
  subjects: Array<FileSubject | OciSubject>;
};

function safeName(name: string): string {
  if (!name || basename(name) !== name || name === "." || name === "..") {
    throw new Error(`unsafe evidence filename: ${name}`);
  }
  return name;
}

function sha256(path: string): string {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function regularFile(path: string, name: string): number {
  const stats = lstatSync(path);
  if (!stats.isFile() || stats.isSymbolicLink()) throw new Error(`not a regular file: ${name}`);
  return stats.size;
}

function validateSbom(path: string, name: string): void {
  regularFile(path, name);
  const document = JSON.parse(readFileSync(path, "utf8")) as Record<string, unknown>;
  if (
    document.bomFormat !== "CycloneDX" ||
    typeof document.specVersion !== "string" ||
    !/^1\.[4-7]$/.test(document.specVersion) ||
    typeof document.version !== "number"
  ) {
    throw new Error(`invalid CycloneDX SBOM: ${name}`);
  }
}

function validateIdentity(release: string, commit: string): void {
  if (!RELEASE.test(release)) throw new Error(`invalid release: ${release}`);
  if (!COMMIT.test(commit)) throw new Error("commit must be a complete lowercase SHA-1");
}

export function createFileEvidence(
  release: string,
  commit: string,
  root: string,
  sbomName: string,
  artifactNames: string[],
): ReleaseEvidence {
  validateIdentity(release, commit);
  const sbom = safeName(sbomName);
  const sbomPath = resolve(root, sbom);
  validateSbom(sbomPath, sbom);
  const sbomHash = sha256(sbomPath);
  if (artifactNames.length === 0) throw new Error("at least one file subject is required");
  const names = new Set<string>();
  const subjects = artifactNames
    .map(safeName)
    .sort()
    .map((name): FileSubject => {
      if (names.has(name)) throw new Error(`duplicate subject: ${name}`);
      names.add(name);
      const path = resolve(root, name);
      const size = regularFile(path, name);
      if (size < 1) throw new Error(`empty subject: ${name}`);
      return {
        kind: "file",
        name,
        sha256: sha256(path),
        size,
        sbom: { name: sbom, sha256: sbomHash },
      };
    });
  return { schemaVersion: 1, release, commit, subjects };
}

export function createContainerEvidence(
  release: string,
  commit: string,
  manifest: ContainerReleaseManifest,
  sbomRoot: string,
): ReleaseEvidence {
  validateIdentity(release, commit);
  if (manifest.release !== release || manifest.commit !== commit) {
    throw new Error("container manifest identity does not match the evidence identity");
  }
  const subjects = Object.entries(manifest.images)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([name, image]): OciSubject => {
      if (!OCI_DIGEST.test(image.digest) || !image.reference.endsWith(`@${image.digest}`)) {
        throw new Error(`invalid OCI identity: ${name}`);
      }
      const sbomName = safeName(`${name}.cdx.json`);
      validateSbom(resolve(sbomRoot, sbomName), sbomName);
      return {
        kind: "oci",
        name,
        reference: image.reference,
        digest: image.digest,
        sbom: { name: sbomName, sha256: sha256(resolve(sbomRoot, sbomName)) },
      };
    });
  if (
    subjects.length !== EXPECTED_CONTAINERS.length ||
    subjects.some((subject, index) => subject.name !== EXPECTED_CONTAINERS[index])
  ) {
    throw new Error("container evidence must contain the five first-party images");
  }
  return { schemaVersion: 1, release, commit, subjects };
}

export function verifyReleaseEvidence(evidence: ReleaseEvidence, root: string): void {
  validateIdentity(evidence.release, evidence.commit);
  if (evidence.schemaVersion !== 1 || evidence.subjects.length === 0) {
    throw new Error("invalid release evidence schema");
  }
  const ociSubjects = evidence.subjects.filter(
    (subject): subject is OciSubject => subject.kind === "oci",
  );
  if (
    ociSubjects.length > 0 &&
    (ociSubjects.length !== evidence.subjects.length ||
      ociSubjects.length !== EXPECTED_CONTAINERS.length ||
      ociSubjects.some((subject, index) => subject.name !== EXPECTED_CONTAINERS[index]))
  ) {
    throw new Error("container evidence must contain the five first-party images");
  }
  const names = new Set<string>();
  for (const subject of evidence.subjects) {
    if (names.has(subject.name)) throw new Error(`duplicate subject: ${subject.name}`);
    names.add(subject.name);
    const sbomName = safeName(subject.sbom.name);
    if (!SHA256.test(subject.sbom.sha256))
      throw new Error(`invalid SBOM checksum: ${subject.name}`);
    validateSbom(resolve(root, sbomName), sbomName);
    if (sha256(resolve(root, sbomName)) !== subject.sbom.sha256) {
      throw new Error(`SBOM checksum mismatch: ${subject.name}`);
    }
    if (subject.kind === "file") {
      const name = safeName(subject.name);
      const path = resolve(root, name);
      if (regularFile(path, name) !== subject.size || sha256(path) !== subject.sha256) {
        throw new Error(`artifact checksum mismatch: ${name}`);
      }
    } else if (
      !OCI_DIGEST.test(subject.digest) ||
      subject.reference !== `ghcr.io/tutitoos/mailflow-${subject.name}@${subject.digest}`
    ) {
      throw new Error(`invalid OCI identity: ${subject.name}`);
    }
  }
}

function writeExclusive(path: string, evidence: ReleaseEvidence): void {
  writeFileSync(path, `${JSON.stringify(evidence, null, 2)}\n`, { flag: "wx" });
}

if (import.meta.main) {
  const [command, ...args] = process.argv.slice(2);
  try {
    if (command === "files") {
      const [release, commit, root, sbom, output, ...artifacts] = args;
      if (!release || !commit || !root || !sbom || !output || artifacts.length === 0) {
        throw new Error(
          "usage: release-evidence.ts files RELEASE COMMIT ROOT SBOM OUTPUT ARTIFACT...",
        );
      }
      writeExclusive(output, createFileEvidence(release, commit, root, sbom, artifacts));
    } else if (command === "containers") {
      const [release, commit, containerManifest, sbomRoot, output] = args;
      if (!release || !commit || !containerManifest || !sbomRoot || !output) {
        throw new Error(
          "usage: release-evidence.ts containers RELEASE COMMIT CONTAINER_MANIFEST SBOM_ROOT OUTPUT",
        );
      }
      const manifest = JSON.parse(
        readFileSync(containerManifest, "utf8"),
      ) as ContainerReleaseManifest;
      writeExclusive(output, createContainerEvidence(release, commit, manifest, sbomRoot));
    } else if (command === "verify") {
      const [manifestPath, root] = args;
      if (!manifestPath || !root) {
        throw new Error("usage: release-evidence.ts verify MANIFEST ROOT");
      }
      verifyReleaseEvidence(
        JSON.parse(readFileSync(manifestPath, "utf8")) as ReleaseEvidence,
        root,
      );
    } else {
      throw new Error("expected files, containers, or verify command");
    }
  } catch (error) {
    console.error(error instanceof Error ? error.message : "release evidence operation failed");
    process.exit(1);
  }
}
