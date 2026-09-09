import { afterEach, describe, expect, it, vi } from "vitest";
import {
  desktopCacheKey,
  hasDesktopOfflineAccounts,
  readDesktopCache,
  removeDesktopAccount,
  storeDesktopAccounts,
} from "./desktop-cache";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));

afterEach(() => {
  invoke.mockReset();
  vi.unstubAllGlobals();
});

function markDesktop() {
  vi.stubGlobal("window", {
    sessionStorage: { getItem: () => "1" },
  });
}

describe("desktop cache bridge", () => {
  it("does not expose native commands to ordinary browsers", async () => {
    vi.stubGlobal("window", { sessionStorage: { getItem: () => null } });
    expect(await hasDesktopOfflineAccounts()).toBe(false);
    expect(invoke).not.toHaveBeenCalled();
  });

  it("uses bounded commands without putting private lookup values in URLs", async () => {
    markDesktop();
    invoke.mockResolvedValueOnce({ items: [] }).mockResolvedValueOnce(undefined);
    const key = desktopCacheKey("from:private@example.test", undefined);

    await readDesktopCache("account-a", "search", key);
    await storeDesktopAccounts([{ id: "account-a" }]);

    expect(invoke.mock.calls).toEqual([
      ["offline_cache_read", { accountId: "account-a", kind: "search", cacheKey: key }],
      ["offline_cache_store_accounts", { accounts: [{ id: "account-a" }] }],
    ]);
  });

  it("surfaces account erasure failures to the disconnect flow", async () => {
    markDesktop();
    invoke.mockRejectedValueOnce(new Error("keychain unavailable"));

    await expect(removeDesktopAccount("account-a")).rejects.toThrow("keychain unavailable");
  });
});
