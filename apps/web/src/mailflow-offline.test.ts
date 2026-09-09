import { afterEach, describe, expect, it, vi } from "vitest";
import { disconnectAccount, loadInboxPage, loadMailAccounts } from "./mailflow-api";

const { nativeInvoke } = vi.hoisted(() => ({ nativeInvoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke: nativeInvoke }));

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });

afterEach(() => {
  nativeInvoke.mockReset();
  vi.unstubAllGlobals();
});

function markDesktop() {
  vi.stubGlobal("window", {
    sessionStorage: { getItem: () => "1" },
  });
}

describe("Mailflow desktop offline data", () => {
  it("opens cached accounts when the installation is unreachable", async () => {
    markDesktop();
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
    nativeInvoke
      .mockRejectedValueOnce(new Error("installation unavailable"))
      .mockResolvedValueOnce([
        {
          id: "account-a",
          provider: "google",
          displayName: "Fixture",
          syncState: "idle",
          disabledAt: null,
          capabilities: {},
        },
      ]);

    await expect(loadMailAccounts()).resolves.toMatchObject({
      items: [{ id: "account-a" }],
    });
    expect(nativeInvoke).toHaveBeenNthCalledWith(2, "offline_cache_list_accounts", {});
  });

  it("writes successful inbox pages and falls back to the same cursor offline", async () => {
    markDesktop();
    const page = { items: [], nextCursor: "opaque-next" };
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json(page))
      .mockRejectedValueOnce(new TypeError("offline"));
    vi.stubGlobal("fetch", fetch);
    nativeInvoke
      .mockResolvedValueOnce({ locale: "en", accessToken: "e30.eyJleHAiOjF9.signature" })
      .mockResolvedValueOnce(undefined)
      .mockResolvedValueOnce({ locale: "en", accessToken: "e30.eyJleHAiOjF9.signature" })
      .mockResolvedValueOnce(page);

    await expect(loadInboxPage("account-a", "primary")).resolves.toEqual(page);
    await expect(loadInboxPage("account-a", "primary")).resolves.toEqual(page);

    const key = JSON.stringify(["primary", ""]);
    expect(nativeInvoke.mock.calls).toEqual([
      ["native_session_current"],
      [
        "offline_cache_write",
        { accountId: "account-a", kind: "inbox", cacheKey: key, value: page },
      ],
      ["native_session_current"],
      ["offline_cache_read", { accountId: "account-a", kind: "inbox", cacheKey: key }],
    ]);
  });

  it("destroys the account cache after a successful disconnect", async () => {
    markDesktop();
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValueOnce(json({ account: { id: "account-a" }, remoteRevoked: true })),
    );
    nativeInvoke
      .mockResolvedValueOnce({ locale: "en", accessToken: "e30.eyJleHAiOjF9.signature" })
      .mockResolvedValueOnce(undefined);

    await disconnectAccount("account-a");

    expect(nativeInvoke).toHaveBeenCalledWith("offline_cache_remove_account", {
      accountId: "account-a",
    });
  });
});
