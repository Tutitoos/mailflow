export type DesktopReleaseChannel = "stable" | "beta";

export interface DesktopReleaseManifestInput {
  version: string;
  channel: DesktopReleaseChannel;
  publishedAt: string;
  notes: string;
  artifactUrl: string;
  signature: string;
}

const BUNDLE_IDENTIFIER = "dev.tutitoos.mailflow";
const TARGET = "darwin-universal";
const MAX_NOTES_CHARACTERS = 4_000;
const MAX_SIGNATURE_CHARACTERS = 16_384;
const RELEASE_VERSION = /^\d+\.\d+\.\d+$/;
const BETA_VERSION = /^\d+\.\d+\.\d+-beta\.\d+(?:\.[0-9A-Za-z-]+)*$/;

function hasUnsafeControlCharacters(value: string): boolean {
  return [...value].some((character) => character < " " && !["\n", "\r", "\t"].includes(character));
}

function assertValidInput(input: DesktopReleaseManifestInput): URL {
  const acceptedVersion =
    input.channel === "stable"
      ? RELEASE_VERSION.test(input.version)
      : BETA_VERSION.test(input.version);
  if (!acceptedVersion) throw new Error("release version does not match its channel");
  if (input.notes.length > MAX_NOTES_CHARACTERS || hasUnsafeControlCharacters(input.notes)) {
    throw new Error("release notes are invalid");
  }
  if (
    input.signature.length < 32 ||
    input.signature.length > MAX_SIGNATURE_CHARACTERS ||
    hasUnsafeControlCharacters(input.signature)
  ) {
    throw new Error("updater signature is invalid");
  }
  const publishedAt = new Date(input.publishedAt);
  if (Number.isNaN(publishedAt.valueOf())) throw new Error("published timestamp is invalid");
  const artifactUrl = new URL(input.artifactUrl);
  if (
    artifactUrl.protocol !== "https:" ||
    artifactUrl.username ||
    artifactUrl.password ||
    artifactUrl.hash
  ) {
    throw new Error("artifact URL must be credential-free HTTPS");
  }
  return artifactUrl;
}

export function buildDesktopReleaseManifest(input: DesktopReleaseManifestInput) {
  const artifactUrl = assertValidInput(input);
  return {
    version: input.version,
    notes: input.notes,
    pub_date: new Date(input.publishedAt).toISOString(),
    mailflow: {
      channel: input.channel,
      bundleIdentifier: BUNDLE_IDENTIFIER,
      target: TARGET,
    },
    platforms: {
      [TARGET]: {
        url: artifactUrl.toString(),
        signature: input.signature.trim(),
      },
    },
  };
}

function option(name: string): string {
  const index = Bun.argv.indexOf(name);
  const value = index >= 0 ? Bun.argv[index + 1] : undefined;
  if (!value || value.startsWith("--")) throw new Error(`missing ${name}`);
  return value;
}

if (import.meta.main) {
  const signaturePath = option("--signature-file");
  const outputPath = option("--output");
  const channel = option("--channel");
  if (channel !== "stable" && channel !== "beta") throw new Error("invalid --channel");
  const manifest = buildDesktopReleaseManifest({
    version: option("--version"),
    channel,
    publishedAt: option("--published-at"),
    notes: option("--notes"),
    artifactUrl: option("--artifact-url"),
    signature: await Bun.file(signaturePath).text(),
  });
  await Bun.write(outputPath, `${JSON.stringify(manifest, null, 2)}\n`);
}
