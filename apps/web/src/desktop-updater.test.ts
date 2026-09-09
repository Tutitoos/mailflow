import { describe, expect, it } from "vitest";
import {
  type DesktopUpdateState,
  desktopUpdateErrorKey,
  desktopUpdatePercent,
} from "./desktop-updater";

function state(overrides: Partial<DesktopUpdateState> = {}): DesktopUpdateState {
  return {
    configured: true,
    currentVersion: "1.0.0",
    channel: "stable",
    phase: "downloading",
    candidate: null,
    downloadedBytes: 0,
    totalBytes: null,
    errorCode: null,
    ...overrides,
  };
}

describe("desktop updater presentation", () => {
  it("bounds determinate progress and preserves unknown totals", () => {
    expect(desktopUpdatePercent(state())).toBeNull();
    expect(desktopUpdatePercent(state({ downloadedBytes: 50, totalBytes: 200 }))).toBe(25);
    expect(desktopUpdatePercent(state({ downloadedBytes: 250, totalBytes: 200 }))).toBe(100);
  });

  it("maps only known native errors to specific localized copy", () => {
    expect(desktopUpdateErrorKey("update_downgrade_rejected")).toBe(
      "admin.updater.error.downgrade",
    );
    expect(desktopUpdateErrorKey("private-native-detail")).toBe("admin.updater.error.unknown");
    expect(desktopUpdateErrorKey(null)).toBe("admin.updater.error.unknown");
  });
});
