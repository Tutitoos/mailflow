import { afterEach, describe, expect, it, vi } from "vitest";
import {
  activateDesktopSession,
  beginDesktopPasskey,
  currentDesktopSession,
  finishDesktopPasskey,
  logoutDesktopSession,
  nativeRequestHeaders,
} from "./desktop-session";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));

afterEach(() => {
  invoke.mockReset();
  vi.unstubAllGlobals();
});

function markDesktop(active = true) {
  vi.stubGlobal("window", {
    sessionStorage: { getItem: () => (active ? "1" : null) },
  });
}

describe("native desktop sessions", () => {
  it("keeps ordinary browsers away from native session commands", async () => {
    markDesktop(false);
    expect(await nativeRequestHeaders()).toEqual({});
    expect(await currentDesktopSession()).toBeNull();
    expect(invoke).not.toHaveBeenCalled();
  });

  it("moves the signed refresh session directly into Keychain", async () => {
    markDesktop();
    invoke.mockResolvedValue({ locale: "es", accessToken: "header.payload.signature" });
    const response = new Response(null, { headers: { "set-auth-token": "signed.session" } });

    expect(await activateDesktopSession(response)).toEqual({
      locale: "es",
      accessToken: "header.payload.signature",
    });
    expect(invoke).toHaveBeenCalledWith("native_session_activate", {
      sessionToken: "signed.session",
    });
  });

  it("attaches the non-secret per-installation identity only to native authentication", async () => {
    markDesktop();
    invoke.mockResolvedValue("a".repeat(64));
    expect(await nativeRequestHeaders()).toEqual({
      "x-mailflow-native": "1",
      "x-mailflow-installation": "a".repeat(64),
    });
    expect(invoke).toHaveBeenCalledWith("native_session_identity");
  });

  it("uses the destructive native logout command", async () => {
    markDesktop();
    invoke.mockResolvedValue(undefined);
    expect(await logoutDesktopSession()).toBe(true);
    expect(invoke).toHaveBeenCalledWith("native_session_logout");
  });

  it("keeps authenticated passkey registration behind the native bridge", async () => {
    markDesktop();
    const options = { challenge: "sanitized-challenge" };
    invoke.mockResolvedValueOnce(options).mockResolvedValueOnce(undefined);

    expect(await beginDesktopPasskey("Mailflow")).toEqual(options);
    expect(await finishDesktopPasskey("Mailflow", { id: "credential" })).toBe(true);
    expect(invoke.mock.calls).toEqual([
      ["native_passkey_begin", { name: "Mailflow" }],
      ["native_passkey_finish", { name: "Mailflow", response: { id: "credential" } }],
    ]);
  });
});
