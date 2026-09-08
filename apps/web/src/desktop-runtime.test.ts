import { describe, expect, it } from "vitest";
import { desktopEntryState } from "./desktop-runtime";

describe("desktop runtime marker", () => {
  it("removes the marker without discarding fixed OAuth results", () => {
    expect(
      desktopEntryState(
        "https://mail.example.test/settings/accounts?desktop=1&google=connected&sync=pending",
        false,
      ),
    ).toEqual({
      active: true,
      cleanPath: "/settings/accounts?google=connected&sync=pending",
      marked: true,
    });
  });

  it("keeps an established desktop session active across routes", () => {
    expect(desktopEntryState("https://mail.example.test/admin/metrics", true)).toEqual({
      active: true,
      cleanPath: "/admin/metrics",
      marked: false,
    });
  });

  it("does not mark ordinary browser sessions", () => {
    expect(desktopEntryState("https://mail.example.test/", false).active).toBe(false);
  });
});
