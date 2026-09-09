import { describe, expect, test } from "bun:test";
import { buildDesktopReleaseManifest } from "./desktop-release-manifest";

const valid = {
  version: "1.2.3",
  channel: "stable" as const,
  publishedAt: "2026-09-09T12:00:00.000Z",
  notes: "Mailflow 1.2.3",
  artifactUrl:
    "https://github.com/Tutitoos/mailflow/releases/download/v1.2.3/Mailflow_1.2.3_universal.app.tar.gz",
  signature: "signed-updater-payload".repeat(4),
};

describe("desktop release manifest", () => {
  test("emits the closed Mailflow updater contract", () => {
    expect(buildDesktopReleaseManifest(valid)).toEqual({
      version: "1.2.3",
      notes: "Mailflow 1.2.3",
      pub_date: "2026-09-09T12:00:00.000Z",
      mailflow: {
        channel: "stable",
        bundleIdentifier: "dev.tutitoos.mailflow",
        target: "darwin-universal",
      },
      platforms: {
        "darwin-universal": {
          url: valid.artifactUrl,
          signature: valid.signature,
        },
      },
    });
  });

  test("keeps stable and beta channels separate", () => {
    expect(() => buildDesktopReleaseManifest({ ...valid, version: "1.2.3-beta.1" })).toThrow(
      "channel",
    );
    expect(
      buildDesktopReleaseManifest({
        ...valid,
        version: "1.2.3-beta.1",
        channel: "beta",
      }).mailflow.channel,
    ).toBe("beta");
  });

  test("rejects unsafe metadata and download locations", () => {
    expect(() =>
      buildDesktopReleaseManifest({ ...valid, artifactUrl: "http://example.test/app.tar.gz" }),
    ).toThrow("HTTPS");
    expect(() =>
      buildDesktopReleaseManifest({ ...valid, artifactUrl: "https://user@example.test/app" }),
    ).toThrow("HTTPS");
    expect(() => buildDesktopReleaseManifest({ ...valid, notes: "unsafe\u0000note" })).toThrow(
      "notes",
    );
    expect(() => buildDesktopReleaseManifest({ ...valid, signature: "short" })).toThrow(
      "signature",
    );
  });
});
