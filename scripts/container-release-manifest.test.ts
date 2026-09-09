import { describe, expect, test } from "bun:test";
import { createContainerReleaseManifest, type ImageRecord } from "./container-release-manifest";

const names = ["api", "auth", "backup", "web", "worker"] as const;
const digest = `sha256:${"a".repeat(64)}`;
const records = names.map(
  (name): ImageRecord => ({ name, image: `ghcr.io/tutitoos/mailflow-${name}`, digest }),
);

describe("container release manifest", () => {
  test("creates deterministic digest-pinned references", () => {
    const manifest = createContainerReleaseManifest(
      [...records].reverse(),
      "v1.2.3-beta.4",
      "b".repeat(40),
    );
    expect(Object.keys(manifest.images)).toEqual(names);
    expect(manifest.images.worker?.reference).toBe(`ghcr.io/tutitoos/mailflow-worker@${digest}`);
  });

  test("rejects missing and duplicate components", () => {
    expect(() =>
      createContainerReleaseManifest(records.slice(1), "v1.2.3", "b".repeat(40)),
    ).toThrow("expected 5 image records");
    expect(() =>
      createContainerReleaseManifest(
        [...records.slice(0, 4), records[0] as ImageRecord],
        "v1.2.3",
        "b".repeat(40),
      ),
    ).toThrow("duplicate image");
  });

  test("rejects mutable, mismatched, or malformed identities", () => {
    expect(() =>
      createContainerReleaseManifest(
        records.map((record) =>
          record.name === "web" ? { ...record, image: "ghcr.io/tutitoos/mailflow-api" } : record,
        ),
        "v1.2.3",
        "b".repeat(40),
      ),
    ).toThrow("invalid image reference");
    expect(() => createContainerReleaseManifest(records, "latest", "b".repeat(40))).toThrow(
      "invalid release",
    );
    expect(() => createContainerReleaseManifest(records, "v1.2.3", "short")).toThrow(
      "complete lowercase SHA-1",
    );
  });
});
