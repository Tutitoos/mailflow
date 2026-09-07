import type { Locale } from "./i18n";

export type AuthErrorCode =
  | "bootstrap_rejected"
  | "invalid_credentials"
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
    },
    body: JSON.stringify({
      name: input.name,
      email: input.email,
      password: input.password,
      locale: input.locale,
    }),
  });
  if (response.ok) return;
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
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  if (response.ok) return;
  if (response.status === 400 || response.status === 401 || response.status === 403) {
    throw new AuthRequestError("invalid_credentials");
  }
  throw new AuthRequestError("service_unavailable");
}
