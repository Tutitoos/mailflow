export type MailAccount = {
  id: string;
  provider: "google" | "microsoft" | "imap";
  displayName: string;
  syncState: "pending" | "syncing" | "idle" | "error" | "disabled";
  disabledAt: string | null;
  capabilities: Record<string, boolean>;
};

export type MailTLSMode = "implicit" | "starttls";

export type IMAPAccountInput = {
  displayName: string;
  username: string;
  password: string;
  imap: { host: string; port: number; tlsMode: MailTLSMode };
  smtp: { host: string; port: number; tlsMode: MailTLSMode };
};

export type IMAPFolderState = {
  mailboxId: string;
  remoteId: string;
  name: string;
  role: "inbox" | "sent" | "drafts" | "trash" | "junk" | "archive" | "all" | "";
  selectable: boolean;
  subscribed: boolean;
  namespacePrefix: string;
  delimiter: string | null;
  uidNext: number | null;
  uidValidity: number | null;
  nextUid: number | null;
  cursorState: "active" | "resync_required" | "not_selectable" | "missing";
  cursorVersion: number;
  invalidatedAt: string | null;
  invalidationReason: "uid_validity_changed" | null;
};

export type IMAPFolderDiscoveryResult = {
  folders: IMAPFolderState[];
  reconciliationRequired: boolean;
};

const legacyMailCapabilities = new Set([
  "actions",
  "attachments",
  "categories",
  "drafts",
  "folders",
  "labels",
  "search",
  "send",
  "threads",
]);

export function accountSupports(
  account: Pick<MailAccount, "provider"> & { capabilities?: Record<string, boolean> },
  capability: string,
) {
  const declared = account.capabilities?.[capability];
  if (typeof declared === "boolean") return declared;
  return account.provider !== "imap" && legacyMailCapabilities.has(capability);
}

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

export type SearchResult = {
  id: string;
  threadId: string;
  accountId: string;
  senderName: string;
  senderAddress: string;
  subject: string;
  preview: string;
  sentAt: string;
  isRead: boolean;
  isStarred: boolean;
  isImportant: boolean;
  hasAttachment: boolean;
  attachmentCount: number;
  rank: number;
};

export type SearchPage = { items: SearchResult[]; nextCursor: string | null };

export type MailActionKind =
  | "mark_read"
  | "mark_unread"
  | "star"
  | "unstar"
  | "mark_important"
  | "mark_unimportant"
  | "move_to_trash"
  | "restore_from_trash"
  | "archive"
  | "add_label"
  | "remove_label";

export type MessageAddress = {
  role: "from" | "sender" | "reply_to" | "to" | "cc" | "bcc";
  position: number;
  displayName: string | null;
  address: string;
};

export type MessageAttachment = {
  id: string;
  position: number;
  filename: string | null;
  mediaType: string;
  disposition: "attachment" | "inline";
  sizeBytes: number;
};

export type ConversationMessage = {
  id: string;
  threadId: string;
  accountId: string;
  subject: string;
  bodyText: string;
  bodyHtml: string;
  sentAt: string;
  isRead: boolean;
  isStarred: boolean;
  isImportant: boolean;
  addresses: MessageAddress[];
  attachments: MessageAttachment[];
};

export type ConversationPage = {
  thread: {
    id: string;
    accountId: string;
    category: MailCategory;
    messageCount: number;
  };
  messages: ConversationMessage[];
  nextCursor: string | null;
};

export type DraftRecipient = {
  role: "to" | "cc" | "bcc";
  position?: number;
  displayName?: string | null;
  address: string;
};

export type DraftAttachment = {
  objectId: string;
  filename: string | null;
  mediaType: string;
  sizeBytes: number;
};

export type DraftContent = {
  accountId: string;
  expectedRevision?: number;
  subject: string;
  bodyText: string;
  bodyHtml: string;
  recipients: DraftRecipient[];
  attachments: DraftAttachment[];
  mode: "new" | "reply" | "forward";
  sourceMessageId?: string;
};

export type MailDraft = DraftContent & {
  id: string;
  localRevision: number;
  syncedRevision: number;
  syncStatus: "queued" | "syncing" | "synced" | "conflict" | "discarded";
  remoteCheckpointAt: string;
  createdAt: string;
  updatedAt: string;
};

export type MailDelivery = {
  id: string;
  accountId: string;
  draftId: string;
  status: "prepared" | "sending" | "sent" | "ambiguous";
  remoteId: string | null;
};

export type MailEvent = {
  version: 1;
  cursor: string;
  type:
    | "mail.changed"
    | "sync.progress"
    | "draft.changed"
    | "admin.alert"
    | "admin.log"
    | "system.status"
    | "translations.changed"
    | "system.resync_required";
  timestamp: string;
  payload: Record<string, unknown>;
};

export type SentryTelemetrySummary = {
  traces: number;
  spans: number;
  profiles: number;
  replays: number;
  replaySegments: number;
  replayEnabled: boolean;
};

export type TranslationAdminSummary = {
  defaultLocale: "en";
  revision: number;
  catalogs: Record<"en" | "es", Record<string, { value: string; sourceHash: string }>>;
  diagnostics: {
    missingEnglish: string[];
    missingSpanish: string[];
    staleSpanish: string[];
    invalidIcu: string[];
    unknownKeys: string[];
    privateValues: string[];
  };
};

export type AdminHealthState = "healthy" | "degraded" | "blocked" | "stale";

export type QueueStats = { ready: number; pending: number; retry: number; dead: number };

export type AdminStatus = {
  state: AdminHealthState;
  version: string;
  goVersion: string;
  checkedAt: string;
  components: Array<{
    name: "api" | "postgres" | "redis" | "worker" | "queue";
    status: AdminHealthState;
    detail: string;
    checkedAt: string;
    lastObservedAt?: string;
  }>;
  queue: QueueStats;
};

export type AdminOperation = {
  id: string;
  action: "queue.retry_sync";
  result: "requested" | "queued" | "already_running" | "failed";
  createdAt: string;
  updatedAt: string;
};

export type AdminQueueOverview = { stats: QueueStats; operations: AdminOperation[] };

export type AdminCDNStatus = {
  attachmentObjects: number;
  attachmentBytes: number;
  sentryObjects: number;
  sentryBytes: number;
  missingObjects: number;
};

export type AdminBackupStatus = {
  configured: boolean;
  state: AdminHealthState;
  runtime?: {
    enabled: boolean;
    repositoryKind: "local" | "s3";
    schedule: string;
    timezone: string;
    nextRunAt: string;
    heartbeatAt: string;
  };
  lastSuccessAt?: string;
  runs: Array<{
    id: string;
    trigger: "scheduled" | "command";
    repositoryKind: "local" | "s3";
    state: "running" | "succeeded" | "failed";
    snapshotId?: string;
    errorCode?: string;
    fileCount: number;
    byteCount: number;
    scheduledFor: string;
    startedAt: string;
    completedAt?: string;
  }>;
};

export type AdminAlertStatus = {
  configured: boolean;
  incidents: Array<{
    id: string;
    policy:
      | "provider_auth"
      | "sync_backlog"
      | "disk"
      | "backup"
      | "sentry_ingestion"
      | "service_health"
      | "test";
    source: string;
    code: string;
    state: "active" | "recovered";
    openedAt: string;
    lastSeenAt: string;
    recoveredAt?: string;
    cooldownUntil: string;
  }>;
  deliveries: Array<{
    id: string;
    incidentId: string;
    kind: "incident" | "reminder" | "recovery" | "test";
    channel: "smtp" | "connected_account";
    status: "pending" | "sent" | "failed" | "suppressed";
    errorCode?: string;
    createdAt: string;
    completedAt?: string;
  }>;
};

export type AdminMetric = {
  bucket: string;
  resolution: "minute" | "hour" | "day";
  name: string;
  kind: "counter" | "gauge" | "histogram";
  labels?: Record<string, string>;
  value: number;
  count: number;
  min: number;
  max: number;
  p50?: number;
  p95?: number;
  p99?: number;
  average?: number;
  ratePerSecond?: number;
};

export type AdminLogEntry = {
  id: number;
  occurredAt: string;
  service: string;
  module: string;
  level: "debug" | "info" | "warning" | "error";
  event: string;
  requestId?: string;
};

export type AdminSentryIssue = {
  id: string;
  component: string;
  environment: string;
  title: string;
  status: "unresolved" | "resolved" | "ignored";
  firstSeenAt: string;
  lastSeenAt: string;
  eventCount: number;
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

export async function loadAccountConnections(signal?: AbortSignal) {
  const [google, microsoft, accounts] = await Promise.all([
    request<{ configured: boolean; setup: string }>("/oauth/google/status", { signal }),
    request<{ configured: boolean; setup: string }>("/oauth/microsoft/status", { signal }),
    request<{ items: MailAccount[] }>("/accounts", { signal }),
  ]);
  return {
    status: { google, microsoft },
    accounts: accounts.items,
  };
}

export async function probeIMAPAccount(input: IMAPAccountInput) {
  return request<{ capabilities: Record<string, boolean> }>("/accounts/imap/probe", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export async function connectIMAPAccount(input: IMAPAccountInput) {
  return request<MailAccount>("/accounts/imap", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export async function discoverIMAPFolders(accountId: string) {
  return request<IMAPFolderDiscoveryResult>(
    `/accounts/${encodeURIComponent(accountId)}/imap/folders/discover`,
    { method: "POST" },
  );
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

export async function createMicrosoftAuthorization(reconsent = false) {
  const result = await request<{ authorizationUrl: string }>("/oauth/microsoft/start", {
    method: "POST",
    body: JSON.stringify({ reconsent }),
  });
  return result.authorizationUrl;
}

export async function startMicrosoftConnection(reconsent = false) {
  const authorizationUrl = await createMicrosoftAuthorization(reconsent);
  window.location.assign(authorizationUrl);
}

export async function disconnectAccount(accountId: string) {
  return request<{
    account: MailAccount;
    remoteRevoked: boolean;
    credentialsRemoved?: boolean;
    revocationUrl?: string;
  }>(`/accounts/${accountId}`, {
    method: "DELETE",
  });
}

export async function refreshAccountCredentials(accountId: string) {
  return request<MailAccount>(`/accounts/${accountId}/refresh`, { method: "POST" });
}

export async function loadMailAccounts(signal?: AbortSignal) {
  return request<{ items: MailAccount[] }>("/accounts", { signal });
}

export async function loadSentryTelemetry(signal?: AbortSignal) {
  return request<SentryTelemetrySummary>("/admin/sentry/telemetry", { signal });
}

export async function loadAdminStatus(signal?: AbortSignal) {
  return request<AdminStatus>("/admin/status", { signal });
}

export async function loadAdminQueue(signal?: AbortSignal) {
  return request<AdminQueueOverview>("/admin/queue", { signal });
}

export async function retryAdminQueue(accountId: string, idempotencyKey: string) {
  return request<{ operation: AdminOperation; created: boolean }>("/admin/queue/retry", {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ accountId, confirmation: "retry" }),
  });
}

export async function loadAdminCDNStatus(signal?: AbortSignal) {
  return request<AdminCDNStatus>("/admin/cdn", { signal });
}

export async function loadAdminBackups(signal?: AbortSignal) {
  return request<AdminBackupStatus>("/admin/backups?limit=25", { signal });
}

export async function loadAdminAlerts(signal?: AbortSignal) {
  return request<AdminAlertStatus>("/admin/alerts", { signal });
}

export async function testAdminAlert(idempotencyKey: string) {
  return request<AdminAlertStatus["incidents"][number]>("/admin/alerts/test", {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ confirmation: "send" }),
  });
}

export async function loadAdminMetrics(signal?: AbortSignal) {
  const now = new Date();
  const query = new URLSearchParams({
    resolution: "minute",
    from: new Date(now.getTime() - 60 * 60 * 1000).toISOString(),
    until: new Date(now.getTime() + 60_000).toISOString(),
    limit: "500",
  });
  return request<{ items: AdminMetric[] }>(`/admin/metrics?${query}`, { signal });
}

export async function loadAdminLogs(signal?: AbortSignal) {
  const now = new Date();
  const query = new URLSearchParams({
    from: new Date(now.getTime() - 60 * 60 * 1000).toISOString(),
    until: new Date(now.getTime() + 1000).toISOString(),
    limit: "100",
  });
  return request<{ items: AdminLogEntry[]; dropped: number }>(`/admin/logs?${query}`, {
    signal,
  });
}

export async function loadAdminDebug(signal?: AbortSignal) {
  return request<{ enabled: boolean; enabledUntil: string | null }>("/admin/logs/debug", {
    signal,
  });
}

export async function setAdminDebug(durationSeconds: number) {
  return request<{ enabled: boolean; enabledUntil: string | null }>("/admin/logs/debug", {
    method: "PUT",
    body: JSON.stringify({ durationSeconds }),
  });
}

export async function loadAdminSentryIssues(signal?: AbortSignal) {
  return request<{ items: AdminSentryIssue[] }>("/admin/sentry?limit=100", { signal });
}

export async function setAdminSentryIssue(issueId: string, status: AdminSentryIssue["status"]) {
  return request<AdminSentryIssue>(`/admin/sentry/${encodeURIComponent(issueId)}`, {
    method: "PUT",
    body: JSON.stringify({ status }),
  });
}

export async function loadTranslationCatalog(locale: "en" | "es", signal?: AbortSignal) {
  const response = await fetch(`/api/v1/translations/${locale}`, {
    cache: "no-store",
    signal,
  });
  if (!response.ok) throw new APIError("translations_unavailable");
  return (await response.json()) as {
    locale: "en" | "es";
    defaultLocale: "en";
    revision: number;
    messages: Record<string, string>;
    missingKeys: string[];
  };
}

export async function loadTranslationAdminSummary(signal?: AbortSignal) {
  return request<TranslationAdminSummary>("/admin/translations", { signal });
}

export type TranslationChange = {
  locale: "en" | "es";
  key: string;
  value: string | null;
  sourceHash?: string;
};

export async function validateTranslationChanges(
  expectedRevision: number,
  changes: TranslationChange[],
) {
  return request<TranslationAdminSummary>("/admin/translations/validate", {
    method: "POST",
    body: JSON.stringify({ expectedRevision, changes }),
  });
}

export async function activateTranslationChanges(
  expectedRevision: number,
  changes: TranslationChange[],
) {
  return request<TranslationAdminSummary & { eventPublished: boolean }>("/admin/translations", {
    method: "PUT",
    body: JSON.stringify({ expectedRevision, changes }),
  });
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

export async function loadConversationPage(
  accountId: string,
  threadId: string,
  cursor?: string,
  signal?: AbortSignal,
) {
  const query = new URLSearchParams({ accountId });
  if (cursor) query.set("cursor", cursor);
  return request<ConversationPage>(`/threads/${encodeURIComponent(threadId)}?${query}`, { signal });
}

export async function searchMail(
  accountId: string,
  expression: string,
  cursor?: string,
  signal?: AbortSignal,
) {
  const query = new URLSearchParams({ accountId, q: expression, limit: "50" });
  if (cursor) query.set("cursor", cursor);
  return request<SearchPage>(`/search?${query}`, { signal });
}

export async function createMailActions(
  accountId: string,
  kind: MailActionKind,
  targetIds: string[],
  idempotencyKey: string,
  labelId?: string,
) {
  return request<{
    items: Array<{ targetId: string; actionId?: string; status?: string; error?: string }>;
    partial: boolean;
  }>("/actions", {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ accountId, kind, targetIds, ...(labelId ? { labelId } : {}) }),
  });
}

export async function createDraft(content: DraftContent) {
  return request<MailDraft>("/drafts", { method: "POST", body: JSON.stringify(content) });
}

export async function loadDraft(accountId: string, draftId: string) {
  const query = new URLSearchParams({ accountId });
  return request<MailDraft>(`/drafts/${encodeURIComponent(draftId)}?${query}`);
}

export async function updateDraft(draftId: string, content: DraftContent) {
  return request<MailDraft>(`/drafts/${encodeURIComponent(draftId)}`, {
    method: "PUT",
    body: JSON.stringify(content),
  });
}

export async function checkpointDraft(accountId: string, draftId: string) {
  return request<MailDraft>(`/drafts/${encodeURIComponent(draftId)}/checkpoint`, {
    method: "POST",
    body: JSON.stringify({ accountId }),
  });
}

export async function discardDraft(accountId: string, draftId: string) {
  const query = new URLSearchParams({ accountId });
  return request<MailDraft>(`/drafts/${encodeURIComponent(draftId)}?${query}`, {
    method: "DELETE",
  });
}

export async function sendDraft(
  accountId: string,
  draftId: string,
  expectedRevision: number,
  idempotencyKey: string,
) {
  return request<MailDelivery>("/send", {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ accountId, draftId, expectedRevision }),
  });
}

export async function uploadDraftAttachment(
  accountId: string,
  file: File,
  onProgress: (loaded: number, total: number) => void,
  signal: AbortSignal,
): Promise<DraftAttachment> {
  const token = await accessToken(signal);
  if (signal.aborted) throw new APIError("attachment_upload_cancelled");
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    const fail = (code: string) => reject(new APIError(code));
    request.open("POST", "/api/v1/attachments");
    request.setRequestHeader("authorization", `Bearer ${token}`);
    request.upload.addEventListener("progress", (event) =>
      onProgress(event.loaded, event.lengthComputable ? event.total : file.size),
    );
    request.addEventListener("load", () => {
      if (request.status < 200 || request.status >= 300) {
        try {
          const payload = JSON.parse(request.responseText) as { code?: unknown };
          fail(typeof payload.code === "string" ? payload.code : "attachment_upload_failed");
        } catch {
          fail("attachment_upload_failed");
        }
        return;
      }
      try {
        resolve(JSON.parse(request.responseText) as DraftAttachment);
      } catch {
        fail("attachment_upload_failed");
      }
    });
    request.addEventListener("error", () => fail("attachment_upload_failed"));
    request.addEventListener("abort", () => fail("attachment_upload_cancelled"));
    signal.addEventListener("abort", () => request.abort(), { once: true });
    const body = new FormData();
    body.set("accountId", accountId);
    body.set("file", file);
    request.send(body);
  });
}

export async function downloadMessageAttachment(
  attachmentId: string,
  onProgress: (loaded: number, total: number) => void,
  signal: AbortSignal,
): Promise<Blob> {
  const token = await accessToken(signal);
  const response = await fetch(`/api/v1/attachments/${encodeURIComponent(attachmentId)}`, {
    credentials: "include",
    cache: "no-store",
    headers: { authorization: `Bearer ${token}` },
    signal,
  });
  if (!response.ok) {
    const payload = (await response.json().catch(() => null)) as { code?: unknown } | null;
    throw new APIError(
      typeof payload?.code === "string" ? payload.code : "attachment_download_failed",
    );
  }
  const total = Number(response.headers.get("content-length") ?? 0);
  if (!response.body) return response.blob();
  const reader = response.body.getReader();
  const chunks: Uint8Array<ArrayBuffer>[] = [];
  let loaded = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (value) {
      const copy = new Uint8Array(value);
      chunks.push(copy);
      loaded += copy.byteLength;
      onProgress(loaded, total || loaded);
    }
  }
  return new Blob(chunks, {
    type: response.headers.get("content-type") ?? "application/octet-stream",
  });
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
