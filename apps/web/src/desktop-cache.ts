import { isDesktopRuntime } from "./desktop-runtime";

export type DesktopCacheKind = "navigation" | "inbox" | "search" | "conversation";
export const desktopConnectivityEvent = "mailflow:desktop-connectivity";

const desktopOfflineKey = "mailflow.desktop.offline";

async function invokeDesktop<T>(command: string, arguments_: Record<string, unknown> = {}) {
  if (!isDesktopRuntime()) return undefined;
  try {
    const { invoke } = await import("@tauri-apps/api/core");
    return await invoke<T>(command, arguments_);
  } catch {
    return undefined;
  }
}

export async function hasDesktopOfflineAccounts() {
  return (await invokeDesktop<boolean>("offline_cache_has_accounts")) === true;
}

export function isDesktopCacheFallback() {
  try {
    return isDesktopRuntime() && window.sessionStorage.getItem(desktopOfflineKey) === "1";
  } catch {
    return false;
  }
}

export function setDesktopCacheFallback(active: boolean) {
  if (!isDesktopRuntime()) return;
  try {
    if (active) window.sessionStorage.setItem(desktopOfflineKey, "1");
    else window.sessionStorage.removeItem(desktopOfflineKey);
    window.dispatchEvent(
      new CustomEvent(desktopConnectivityEvent, { detail: { offline: active } }),
    );
  } catch {
    // Storage or event restrictions must not hide otherwise usable cached data.
  }
}

export async function readDesktopAccounts<T>() {
  const items = await invokeDesktop<T[]>("offline_cache_list_accounts");
  return items ? { items } : undefined;
}

export async function storeDesktopAccounts(accounts: unknown[]) {
  await invokeDesktop("offline_cache_store_accounts", { accounts });
}

export async function readDesktopCache<T>(
  accountId: string,
  kind: DesktopCacheKind,
  cacheKey: string,
) {
  return invokeDesktop<T>("offline_cache_read", { accountId, kind, cacheKey });
}

export async function writeDesktopCache(
  accountId: string,
  kind: DesktopCacheKind,
  cacheKey: string,
  value: unknown,
) {
  await invokeDesktop("offline_cache_write", { accountId, kind, cacheKey, value });
}

export async function removeDesktopAccount(accountId: string) {
  if (!isDesktopRuntime()) return;
  const { invoke } = await import("@tauri-apps/api/core");
  await invoke("offline_cache_remove_account", { accountId });
}

export function desktopCacheKey(...parts: Array<string | undefined>) {
  return JSON.stringify(parts.map((part) => part ?? ""));
}

export function registerDesktopOfflineShell() {
  if (!isDesktopRuntime() || !("serviceWorker" in navigator)) return;
  void navigator.serviceWorker.register("/desktop-service-worker.js").catch(() => undefined);
}
