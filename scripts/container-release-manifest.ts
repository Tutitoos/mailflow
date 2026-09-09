import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const EXPECTED_IMAGES = ["api", "auth", "backup", "web", "worker"] as const;
const DIGEST = /^sha256:[a-f0-9]{64}$/;
const IMAGE =
  /^ghcr\.io\/tutitoos\/mailflow-(api|auth|backup|web|worker)$/;
const RELEASE = /^v\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)\.\d+)?$/;
const COMMIT = /^[a-f0-9]{40}$/;

export type ImageRecord = {
  name: (typeof EXPECTED_IMAGES)[number];
  image: string;
  digest: string;
};

export type ContainerReleaseManifest = {
  schemaVersion: 1;
  release: string;
  commit: string;
  images: Record<string, { digest: string; reference: string }>;
};

export function createContainerReleaseManifest(
  records: ImageRecord[],
  release: string,
  commit: string,
): ContainerReleaseManifest {
  if (!RELEASE.test(release)) throw new Error(`invalid release: ${release}`);
  if (!COMMIT.test(commit)) throw new Error("commit must be a complete lowercase SHA-1");
  if (records.length !== EXPECTED_IMAGES.length) {
    throw new Error(`expected ${EXPECTED_IMAGES.length} image records`);
  }

  const images: ContainerReleaseManifest["images"] = {};
  for (const record of [...records].sort((left, right) => left.name.localeCompare(right.name))) {
    if (!EXPECTED_IMAGES.includes(record.name)) throw new Error(`unexpected image: ${record.name}`);
    if (images[record.name]) throw new Error(`duplicate image: ${record.name}`);
    const match = IMAGE.exec(record.image);
    if (!match || match[1] !== record.name)
      throw new Error(`invalid image reference: ${record.image}`);
    if (!DIGEST.test(record.digest)) throw new Error(`invalid digest for ${record.name}`);
    images[record.name] = {
      digest: record.digest,
      reference: `${record.image}@${record.digest}`,
    };
  }
  for (const expected of EXPECTED_IMAGES) {
    if (!images[expected]) throw new Error(`missing image: ${expected}`);
  }
  return { schemaVersion: 1, release, commit, images };
}

function readRecords(directory: string): ImageRecord[] {
  return readdirSync(directory)
    .filter((file) => file.endsWith(".json"))
    .map((file) => JSON.parse(readFileSync(resolve(directory, file), "utf8")) as ImageRecord);
}

if (import.meta.main) {
  const [directory, release, commit, output] = process.argv.slice(2);
  if (!directory || !release || !commit || !output) {
    console.error(
      "usage: bun scripts/container-release-manifest.ts RECORDS_DIR RELEASE COMMIT OUTPUT",
    );
    process.exit(2);
  }
  try {
    const manifest = createContainerReleaseManifest(readRecords(directory), release, commit);
    writeFileSync(output, `${JSON.stringify(manifest, null, 2)}\n`, { flag: "wx" });
  } catch (error) {
    console.error(error instanceof Error ? error.message : "manifest generation failed");
    process.exit(1);
  }
}
