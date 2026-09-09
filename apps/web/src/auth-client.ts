import {
  type PublicKeyCredentialCreationOptionsJSON,
  startAuthentication,
  startRegistration,
} from "@simplewebauthn/browser";
import { isDesktopRuntime } from "./desktop-runtime";
import {
  activateDesktopSession,
  beginDesktopPasskey,
  currentDesktopSession,
  finishDesktopPasskey,
  forgetDesktopSession,
  logoutDesktopSession,
  nativeRequestHeaders,
} from "./desktop-session";
import type { Locale } from "./i18n";

export type AuthErrorCode =
  | "bootstrap_rejected"
  | "invalid_credentials"
  | "recovery_rejected"
  | "registration_closed"
  | "service_unavailable";

export class AuthRequestError extends Error {
  constructor(readonly code: AuthErrorCode) {
    super(code);
    this.name = "AuthRequestError";
  }
}

async function send(path: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(path, { ...init, credentials: "include", cache: "no-store" });
  } catch {
    throw new AuthRequestError("service_unavailable");
  }
}

export async function getSetupStatus(signal?: AbortSignal): Promise<boolean> {
  const response = await send("/api/auth/setup/status", { signal });
  if (!response.ok) throw new AuthRequestError("service_unavailable");
  const result = (await response.json()) as { configured?: boolean };
  if (typeof result.configured !== "boolean") {
    throw new AuthRequestError("service_unavailable");
  }
  return result.configured;
}

export async function getSessionLocale(signal?: AbortSignal): Promise<Locale | null> {
  if (isDesktopRuntime()) return (await currentDesktopSession())?.locale ?? null;
  const response = await send("/api/auth/get-session", { signal });
  if (!response.ok) return null;
  const result = (await response.json()) as { user?: { locale?: unknown } } | null;
  if (!result?.user) return null;
  return result.user.locale === "es" ? "es" : "en";
}

export async function createOwner(input: {
  name: string;
  email: string;
  password: string;
  locale: Locale;
  bootstrapToken: string;
}): Promise<void> {
  const response = await send("/api/auth/sign-up/email", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-mailflow-bootstrap-token": input.bootstrapToken,
      ...(await nativeRequestHeaders()),
    },
    body: JSON.stringify({
      name: input.name,
      email: input.email,
      password: input.password,
      locale: input.locale,
    }),
  });
  if (response.ok) {
    await activateDesktopSession(response);
    return;
  }
  if (response.status === 403) throw new AuthRequestError("bootstrap_rejected");
  if (response.status === 409) throw new AuthRequestError("registration_closed");
  if (response.status === 422) {
    try {
      if (await getSetupStatus()) throw new AuthRequestError("registration_closed");
    } catch (error) {
      if (error instanceof AuthRequestError && error.code === "registration_closed") throw error;
    }
  }
  throw new AuthRequestError("service_unavailable");
}

export async function signIn(email: string, password: string): Promise<void> {
  const response = await send("/api/auth/sign-in/email", {
    method: "POST",
    headers: { "content-type": "application/json", ...(await nativeRequestHeaders()) },
    body: JSON.stringify({ email, password }),
  });
  if (response.ok) {
    await activateDesktopSession(response);
    return;
  }
  if (response.status === 400 || response.status === 401 || response.status === 403) {
    throw new AuthRequestError("invalid_credentials");
  }
  throw new AuthRequestError("service_unavailable");
}

export async function signInWithPasskey(): Promise<void> {
  try {
    const optionsResponse = await send("/api/auth/passkey/generate-authenticate-options", {
      headers: await nativeRequestHeaders(),
    });
    if (!optionsResponse.ok) throw new AuthRequestError("invalid_credentials");
    const optionsJSON = await optionsResponse.json();
    const credential = await startAuthentication({ optionsJSON });
    const { clientExtensionResults: _clientExtensionResults, ...response } = credential;
    const verification = await send("/api/auth/passkey/verify-authentication", {
      method: "POST",
      headers: { "content-type": "application/json", ...(await nativeRequestHeaders()) },
      body: JSON.stringify({ response }),
    });
    if (!verification.ok) throw new AuthRequestError("invalid_credentials");
    await activateDesktopSession(verification);
  } catch (error) {
    if (error instanceof AuthRequestError) throw error;
    throw new AuthRequestError("invalid_credentials");
  }
}

export async function recoverOwner(recoveryCode: string, newPassword: string): Promise<void> {
  const response = await send("/api/auth/recover", {
    method: "POST",
    headers: { "content-type": "application/json", ...(await nativeRequestHeaders()) },
    body: JSON.stringify({ recoveryCode, newPassword }),
  });
  if (!response.ok) {
    if (response.status === 400 || response.status === 403 || response.status === 429) {
      throw new AuthRequestError("recovery_rejected");
    }
    throw new AuthRequestError("service_unavailable");
  }
  await forgetDesktopSession();
}

export async function registerPasskey(name = "Mailflow"): Promise<void> {
  try {
    let optionsJSON = await beginDesktopPasskey(name);
    if (!optionsJSON) {
      const query = new URLSearchParams({ name });
      const response = await send(`/api/auth/passkey/generate-register-options?${query}`);
      if (!response.ok) throw new AuthRequestError("service_unavailable");
      optionsJSON = await response.json();
    }
    const credential = await startRegistration({
      optionsJSON: optionsJSON as PublicKeyCredentialCreationOptionsJSON,
    });
    const { clientExtensionResults: _clientExtensionResults, ...response } = credential;
    if (await finishDesktopPasskey(name, response)) return;
    const verification = await send("/api/auth/passkey/verify-registration", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ response, name }),
    });
    if (!verification.ok) throw new AuthRequestError("service_unavailable");
  } catch (error) {
    if (error instanceof AuthRequestError) throw error;
    throw new AuthRequestError("service_unavailable");
  }
}

export async function signOut(): Promise<void> {
  if (await logoutDesktopSession()) return;
  const response = await send("/api/auth/sign-out", {
    method: "POST",
    headers: { "content-type": "application/json" },
  });
  if (!response.ok) throw new AuthRequestError("service_unavailable");
}
