export type MailAccount = {
  id: string;
  provider: "google" | "microsoft" | "imap";
  displayName: string;
  syncState: "pending" | "syncing" | "idle" | "error" | "disabled";
  disabledAt: string | null;
};

export type MailCategory = "primary" | "promotions" | "social" | "notifications" | "forums";

export type Mailbox = {
  id: string;
  accountId: string;
  remoteName: string;
  localName: string | null;
  role: "inbox" | "sent" | "drafts" | "trash" | "junk" | "archive" | "all";
  totalCount: number;
  unreadCount: number;
};

export type MailLabel = {
  id: string;
  accountId: string;
  remoteName: string;
  localName: string | null;
  kind: "system" | "user" | "category";
  category: MailCategory | null;
  color: string | null;
  totalCount: number;
  unreadCount: number;
};

export type InboxThread = {
  id: string;
  accountId: string;
  senderName: string;
  senderAddress: string;
  subject: string;
  preview: string;
  lastMessageAt: string;
  isRead: boolean;
  isStarred: boolean;
  isImportant: boolean;
  category: MailCategory;
  messageCount: number;
  attachmentCount: number;
};

export type InboxPage = { items: InboxThread[]; nextCursor: string | null };

export type MailEvent = {
  version: 1;
  cursor: string;
  type:
    | "mail.changed"
    | "sync.progress"
    | "draft.changed"
    | "admin.alert"
    | "system.status"
    | "system.resync_required";
  timestamp: string;
  payload: Record<string, unknown>;
};

export class APIError extends Error {
  constructor(readonly code: string) {
    super(code);
    this.name = "APIError";
  }
}

async function accessToken(signal?: AbortSignal): Promise<string> {
  const response = await fetch("/api/auth/token", {
    credentials: "include",
    cache: "no-store",
    signal,
  });
  if (!response.ok) throw new APIError("authentication_failed");
  const payload = (await response.json()) as { token?: unknown };
  if (typeof payload.token !== "string") throw new APIError("authentication_failed");
  return payload.token;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = await accessToken(init?.signal ?? undefined);
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

export async function loadMailAccounts(signal?: AbortSignal) {
  return request<{ items: MailAccount[] }>("/accounts", { signal });
}

export async function loadMailNavigation(accountId: string, signal?: AbortSignal) {
  const query = new URLSearchParams({ accountId }).toString();
  const [mailboxes, labels] = await Promise.all([
    request<{ items: Mailbox[] }>(`/mailboxes?${query}`, { signal }),
    request<{ items: MailLabel[] }>(`/labels?${query}`, { signal }),
  ]);
  return { mailboxes: mailboxes.items, labels: labels.items };
}

export async function loadInboxPage(
  accountId: string,
  category: MailCategory,
  cursor?: string,
  signal?: AbortSignal,
) {
  const query = new URLSearchParams({ accountId, category, limit: "50" });
  if (cursor) query.set("cursor", cursor);
  return request<InboxPage>(`/threads?${query}`, { signal });
}

export async function subscribeMailEvents(
  onEvent: (event: MailEvent) => void,
  onConnection: (connected: boolean) => void,
  signal: AbortSignal,
) {
  let socket: WebSocket | undefined;
  let retry: number | undefined;
  let cursor = "";
  const scheduleReconnect = () => {
    if (!signal.aborted) {
      retry = window.setTimeout(() => void connect().catch(scheduleReconnect), 1_500);
    }
  };
  const connect = async () => {
    const token = await accessToken(signal);
    if (signal.aborted) return;
    const url = new URL("/api/v1/events", window.location.href);
    url.protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    if (cursor) url.searchParams.set("cursor", cursor);
    socket = new WebSocket(url, ["mailflow.v1", `mailflow.bearer.${token}`]);
    socket.addEventListener("open", () => onConnection(true));
    socket.addEventListener("close", () => {
      onConnection(false);
      scheduleReconnect();
    });
    socket.addEventListener("message", (message) => {
      try {
        const event = JSON.parse(String(message.data)) as MailEvent;
        if (event.version === 1 && typeof event.cursor === "string") {
          cursor = event.cursor;
          onEvent(event);
        }
      } catch {
        // Ignore malformed/unversioned events; the last valid cursor remains resumable.
      }
    });
  };
  signal.addEventListener(
    "abort",
    () => {
      if (retry !== undefined) window.clearTimeout(retry);
      socket?.close(1000, "page closed");
    },
    { once: true },
  );
  await connect().catch(scheduleReconnect);
}
