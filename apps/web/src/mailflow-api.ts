export type MailAccount = {
  id: string;
  provider: "google" | "microsoft" | "imap";
  displayName: string;
  syncState: "pending" | "syncing" | "idle" | "error" | "disabled";
  disabledAt: string | null;
};

export class APIError extends Error {
  constructor(readonly code: string) {
    super(code);
    this.name = "APIError";
  }
}

async function accessToken(): Promise<string> {
  const response = await fetch("/api/auth/token", { credentials: "include", cache: "no-store" });
  if (!response.ok) throw new APIError("authentication_failed");
  const payload = (await response.json()) as { token?: unknown };
  if (typeof payload.token !== "string") throw new APIError("authentication_failed");
  return payload.token;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = await accessToken();
  const response = await fetch(`/api/v1${path}`, {
    ...init,
    cache: "no-store",
    headers: {
      "content-type": "application/json",
      ...init?.headers,
      authorization: `Bearer ${token}`,
    },
  });
  if (!response.ok) {
    const payload = (await response.json().catch(() => null)) as { code?: unknown } | null;
    throw new APIError(typeof payload?.code === "string" ? payload.code : "request_failed");
  }
  return (await response.json()) as T;
}

export async function loadGoogleAccounts(signal?: AbortSignal) {
  const [status, accounts] = await Promise.all([
    request<{ configured: boolean; setup: string }>("/oauth/google/status", { signal }),
    request<{ items: MailAccount[] }>("/accounts", { signal }),
  ]);
  return { status, accounts: accounts.items.filter((account) => account.provider === "google") };
}

export async function createGoogleAuthorization(reconsent = false) {
  const result = await request<{ authorizationUrl: string }>("/oauth/google/start", {
    method: "POST",
    body: JSON.stringify({ reconsent }),
  });
  return result.authorizationUrl;
}

export async function startGoogleConnection(reconsent = false) {
  const authorizationUrl = await createGoogleAuthorization(reconsent);
  window.location.assign(authorizationUrl);
}

export async function disconnectAccount(accountId: string) {
  return request<{ account: MailAccount; remoteRevoked: boolean }>(`/accounts/${accountId}`, {
    method: "DELETE",
  });
}
