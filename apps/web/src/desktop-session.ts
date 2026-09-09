import { invoke } from "@tauri-apps/api/core";
import { isDesktopRuntime } from "./desktop-runtime";
import type { Locale } from "./i18n";

type NativeSession = {
  locale: Locale;
  accessToken: string;
};

let cachedAccessToken: { token: string; expiresAt: number } | null = null;

function cacheToken(session: NativeSession) {
  const encoded = session.accessToken.split(".")[1];
  if (!encoded) return;
  try {
    const normalized = encoded.replaceAll("-", "+").replaceAll("_", "/");
    const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
    const payload = JSON.parse(atob(padded)) as {
      exp?: unknown;
    };
    if (typeof payload.exp === "number") {
      cachedAccessToken = { token: session.accessToken, expiresAt: payload.exp * 1000 };
    }
  } catch {
    cachedAccessToken = null;
  }
}

export async function nativeRequestHeaders(): Promise<Record<string, string>> {
  if (!isDesktopRuntime()) return {};
  const installation = await invoke<string>("native_session_identity");
  return { "x-mailflow-native": "1", "x-mailflow-installation": installation };
}

export async function activateDesktopSession(response: Response): Promise<NativeSession | null> {
  if (!isDesktopRuntime()) return null;
  const sessionToken = response.headers.get("set-auth-token");
  if (!sessionToken) throw new Error("native_session_missing");
  const session = await invoke<NativeSession>("native_session_activate", { sessionToken });
  cacheToken(session);
  return session;
}

export async function currentDesktopSession(): Promise<NativeSession | null> {
  if (!isDesktopRuntime()) return null;
  const session = await invoke<NativeSession | null>("native_session_current");
  if (session) cacheToken(session);
  else cachedAccessToken = null;
  return session;
}

export async function desktopAccessToken(): Promise<string | null> {
  if (!isDesktopRuntime()) return null;
  if (cachedAccessToken && cachedAccessToken.expiresAt > Date.now() + 30_000) {
    return cachedAccessToken.token;
  }
  return (await currentDesktopSession())?.accessToken ?? null;
}

export async function logoutDesktopSession() {
  if (!isDesktopRuntime()) return false;
  try {
    await invoke("native_session_logout");
  } finally {
    cachedAccessToken = null;
  }
  return true;
}

export async function forgetDesktopSession() {
  if (!isDesktopRuntime()) return;
  try {
    await invoke("native_session_forget_local");
  } finally {
    cachedAccessToken = null;
  }
}

export async function beginDesktopPasskey(name: string): Promise<unknown | null> {
  if (!isDesktopRuntime()) return null;
  return invoke("native_passkey_begin", { name });
}

export async function finishDesktopPasskey(name: string, response: unknown): Promise<boolean> {
  if (!isDesktopRuntime()) return false;
  await invoke("native_passkey_finish", { name, response });
  return true;
}
