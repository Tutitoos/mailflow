import {
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  Clock3,
  Database,
  HardDrive,
  Languages,
  ListRestart,
  RefreshCw,
  Server,
  Settings2,
  ShieldAlert,
  TerminalSquare,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { Button } from "./components/ui/button";
import { installTranslationCatalog, type Locale, type TranslationKey, translate } from "./i18n";
import {
  type AdminBackupStatus,
  type AdminCDNStatus,
  type AdminHealthState,
  type AdminLogEntry,
  type AdminMetric,
  type AdminQueueOverview,
  type AdminSentryIssue,
  type AdminStatus,
  activateTranslationChanges,
  loadAdminBackups,
  loadAdminCDNStatus,
  loadAdminDebug,
  loadAdminLogs,
  loadAdminMetrics,
  loadAdminQueue,
  loadAdminSentryIssues,
  loadAdminStatus,
  loadMailAccounts,
  loadSentryTelemetry,
  loadTranslationAdminSummary,
  loadTranslationCatalog,
  type MailAccount,
  retryAdminQueue,
  type SentryTelemetrySummary,
  setAdminDebug,
  setAdminSentryIssue,
  subscribeMailEvents,
  type TranslationAdminSummary,
  type TranslationChange,
  validateTranslationChanges,
} from "./mailflow-api";
import { Brand } from "./pages";

type Translator = (key: TranslationKey) => string;
type AdminSection =
  | "overview"
  | "accounts"
  | "synchronization"
  | "metrics"
  | "logs"
  | "errors"
  | "translations"
  | "cdn"
  | "backups"
  | "updates"
  | "settings";

const sections: Array<{ id: AdminSection; key: TranslationKey }> = [
  { id: "overview", key: "admin.overview" },
  { id: "accounts", key: "accounts" },
  { id: "synchronization", key: "admin.synchronization" },
  { id: "metrics", key: "metrics" },
  { id: "logs", key: "logs" },
  { id: "errors", key: "errors" },
  { id: "translations", key: "admin.translations" },
  { id: "cdn", key: "admin.cdn" },
  { id: "backups", key: "admin.backups" },
  { id: "updates", key: "admin.updates" },
  { id: "settings", key: "settings" },
];

function validSection(value: string | undefined): AdminSection {
  return sections.some((section) => section.id === value) ? (value as AdminSection) : "overview";
}

function formatNumber(value: number | undefined, locale: Locale = "en") {
  return value === undefined ? "—" : new Intl.NumberFormat(locale).format(value);
}

function formatTime(value: string | undefined, locale: Locale) {
  if (!value) return "—";
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

function formatBytes(value: number | undefined) {
  if (value === undefined) return "—";
  if (value < 1024) return `${value} B`;
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KiB`;
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MiB`;
  return `${(value / 1024 ** 3).toFixed(1)} GiB`;
}

function healthLabel(t: Translator, state: AdminHealthState | undefined) {
  return t(`admin.health.${state ?? "stale"}` as TranslationKey);
}

function healthDetail(t: Translator, detail: string) {
  const key = `admin.health.detail.${detail}` as TranslationKey;
  const translated = t(key);
  return !translated || translated === key ? detail : translated;
}

function HealthBadge({ state, t }: { state: AdminHealthState | undefined; t: Translator }) {
  return (
    <span className={`admin-health admin-health-${state ?? "stale"}`}>
      {state === "healthy" ? <CheckCircle2 size={14} /> : <AlertTriangle size={14} />}
      {healthLabel(t, state)}
    </span>
  );
}

function QueueCards({
  queue,
  t,
  locale,
}: {
  queue: AdminQueueOverview | null;
  t: Translator;
  locale: Locale;
}) {
  const entries: Array<[TranslationKey, number | undefined]> = [
    ["admin.queue.ready", queue?.stats.ready],
    ["admin.queue.pending", queue?.stats.pending],
    ["admin.queue.retry", queue?.stats.retry],
    ["admin.queue.dead", queue?.stats.dead],
  ];
  return (
    <section className="admin-stat-grid" aria-label={t("queue")}>
      {entries.map(([key, value]) => (
        <article key={key}>
          <span>{t(key)}</span>
          <strong>{formatNumber(value, locale)}</strong>
        </article>
      ))}
    </section>
  );
}

function Confirmation({
  title,
  body,
  confirm,
  cancel,
  onConfirm,
  onCancel,
}: {
  title: string;
  body: string;
  confirm: string;
  cancel: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div
      className="admin-confirm"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="admin-confirm-title"
    >
      <strong id="admin-confirm-title">{title}</strong>
      <p>{body}</p>
      <div>
        <Button variant="outline" onClick={onCancel} autoFocus>
          {cancel}
        </Button>
        <Button variant="primary" onClick={onConfirm}>
          {confirm}
        </Button>
      </div>
    </div>
  );
}

export function AdminPage({ locale }: { locale: Locale }) {
  const navigate = useNavigate();
  const params = useParams<{ section?: string }>();
  const section = validSection(params.section);
  const t: Translator = (key) => translate(locale, key);
  const [status, setStatus] = useState<AdminStatus | null>(null);
  const [queue, setQueue] = useState<AdminQueueOverview | null>(null);
  const [accounts, setAccounts] = useState<MailAccount[]>([]);
  const [metrics, setMetrics] = useState<AdminMetric[]>([]);
  const [logs, setLogs] = useState<AdminLogEntry[]>([]);
  const [droppedLogs, setDroppedLogs] = useState(0);
  const [debug, setDebug] = useState<{ enabled: boolean; enabledUntil: string | null } | null>(
    null,
  );
  const [issues, setIssues] = useState<AdminSentryIssue[]>([]);
  const [telemetry, setTelemetry] = useState<SentryTelemetrySummary | null>(null);
  const [translations, setTranslations] = useState<TranslationAdminSummary | null>(null);
  const [cdn, setCDN] = useState<AdminCDNStatus | null>(null);
  const [backups, setBackups] = useState<AdminBackupStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [unavailable, setUnavailable] = useState(false);
  const [confirmAccount, setConfirmAccount] = useState<MailAccount | null>(null);
  const [confirmDebug, setConfirmDebug] = useState(false);
  const [notice, setNotice] = useState<TranslationKey | null>(null);
  const [logLevel, setLogLevel] = useState<"all" | AdminLogEntry["level"]>("all");
  const [translationKey, setTranslationKey] = useState("");
  const [spanishValue, setSpanishValue] = useState("");
  const [candidateValid, setCandidateValid] = useState(false);
  const [translationNotice, setTranslationNotice] = useState<TranslationKey | null>(null);

  const refresh = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    const results = await Promise.allSettled([
      loadAdminStatus(signal),
      loadAdminQueue(signal),
      loadMailAccounts(signal),
      loadAdminMetrics(signal),
      loadAdminLogs(signal),
      loadAdminDebug(signal),
      loadAdminSentryIssues(signal),
      loadSentryTelemetry(signal),
      loadTranslationAdminSummary(signal),
      loadAdminCDNStatus(signal),
      loadAdminBackups(signal),
    ]);
    if (signal?.aborted) return;
    if (results[0].status === "fulfilled") setStatus(results[0].value);
    if (results[1].status === "fulfilled") setQueue(results[1].value);
    if (results[2].status === "fulfilled") setAccounts(results[2].value.items);
    if (results[3].status === "fulfilled") setMetrics(results[3].value.items);
    if (results[4].status === "fulfilled") {
      setLogs(results[4].value.items);
      setDroppedLogs(results[4].value.dropped);
    }
    if (results[5].status === "fulfilled") setDebug(results[5].value);
    if (results[6].status === "fulfilled") setIssues(results[6].value.items);
    if (results[7].status === "fulfilled") setTelemetry(results[7].value);
    if (results[8].status === "fulfilled") setTranslations(results[8].value);
    if (results[9].status === "fulfilled") setCDN(results[9].value);
    if (results[10].status === "fulfilled") setBackups(results[10].value);
    setUnavailable(results[0].status === "rejected");
    setLoading(false);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void refresh(controller.signal);
    return () => controller.abort();
  }, [refresh]);

  useEffect(() => {
    const controller = new AbortController();
    void subscribeMailEvents(
      (event) => {
        if (event.type === "admin.log") {
          void loadAdminLogs(controller.signal).then((page) => {
            setLogs(page.items);
            setDroppedLogs(page.dropped);
          });
        }
      },
      () => undefined,
      controller.signal,
    );
    return () => controller.abort();
  }, []);

  const catalogKeys = useMemo(
    () => Object.keys(translations?.catalogs.en ?? {}).sort(),
    [translations],
  );
  const selectedTranslationKey = translationKey || catalogKeys[0] || "";
  const selectedEnglish = translations?.catalogs.en[selectedTranslationKey];

  const chooseTranslation = (key: string) => {
    setTranslationKey(key);
    setSpanishValue(translations?.catalogs.es[key]?.value ?? "");
    setCandidateValid(false);
    setTranslationNotice(null);
  };

  useEffect(() => {
    if (!translationKey && catalogKeys[0]) {
      setSpanishValue(translations?.catalogs.es[catalogKeys[0]]?.value ?? "");
    }
  }, [catalogKeys, translationKey, translations]);

  const translationChange = (): TranslationChange | null => {
    if (!selectedTranslationKey || !selectedEnglish) return null;
    return {
      locale: "es",
      key: selectedTranslationKey,
      value: spanishValue.trim() || null,
      sourceHash: selectedEnglish.sourceHash,
    };
  };

  const validateCandidate = async () => {
    const change = translationChange();
    if (!translations || !change) return;
    try {
      const candidate = await validateTranslationChanges(translations.revision, [change]);
      const diagnostics = candidate.diagnostics;
      const blocking =
        diagnostics.missingEnglish.length +
          diagnostics.staleSpanish.length +
          diagnostics.invalidIcu.length +
          diagnostics.unknownKeys.length +
          diagnostics.privateValues.length >
        0;
      setCandidateValid(!blocking);
      setTranslationNotice(blocking ? "admin.translations.invalid" : "admin.translations.valid");
    } catch {
      setCandidateValid(false);
      setTranslationNotice("admin.translations.invalid");
    }
  };

  const activateCandidate = async () => {
    const change = translationChange();
    if (!translations || !change || !candidateValid) return;
    try {
      const updated = await activateTranslationChanges(translations.revision, [change]);
      setTranslations(updated);
      const current = await loadTranslationCatalog(locale);
      installTranslationCatalog(current);
      setCandidateValid(false);
      setTranslationNotice("admin.translations.valid");
    } catch {
      setCandidateValid(false);
      setTranslationNotice("admin.translations.invalid");
    }
  };

  const retryAccount = async () => {
    if (!confirmAccount) return;
    try {
      const result = await retryAdminQueue(confirmAccount.id, crypto.randomUUID());
      setNotice(result.created ? "admin.queue.accepted" : "admin.queue.duplicate");
      setConfirmAccount(null);
      setQueue(await loadAdminQueue());
      setStatus(await loadAdminStatus());
    } catch {
      setNotice("admin.queue.failed");
      setConfirmAccount(null);
    }
  };

  const toggleDebug = async () => {
    if (!debug) return;
    try {
      setDebug(await setAdminDebug(debug.enabled ? 0 : 15 * 60));
      setConfirmDebug(false);
    } catch {
      setUnavailable(true);
      setConfirmDebug(false);
    }
  };

  const setIssueStatus = async (issue: AdminSentryIssue, next: AdminSentryIssue["status"]) => {
    try {
      const updated = await setAdminSentryIssue(issue.id, next);
      setIssues((current) => current.map((item) => (item.id === updated.id ? updated : item)));
    } catch {
      setUnavailable(true);
    }
  };

  const filteredLogs = logs.filter((entry) => logLevel === "all" || entry.level === logLevel);
  const metricSummary = metrics.find(
    (point) => point.name === "mailflow_http_request_duration_seconds",
  );

  const renderQueueActions = () => (
    <section className="admin-panel">
      <header>
        <strong>{t("admin.queue.request")}</strong>
        <Button size="sm" onClick={() => navigate("/settings/accounts")}>
          {t("admin.openAccounts")}
        </Button>
      </header>
      <div className="admin-list">
        {accounts.length === 0 && <p>{t("admin.noAccounts")}</p>}
        {accounts.map((account) => (
          <article key={account.id}>
            <span className="provider-icon">{account.provider.slice(0, 1).toUpperCase()}</span>
            <div>
              <strong>{account.displayName}</strong>
              <small>
                {account.provider} · {account.syncState}
              </small>
            </div>
            <Button variant="outline" size="sm" onClick={() => setConfirmAccount(account)}>
              <ListRestart size={14} />
              {t("admin.queue.request")}
            </Button>
          </article>
        ))}
      </div>
    </section>
  );

  const renderOperations = () => (
    <section className="admin-panel">
      <header>
        <strong>{t("admin.operations")}</strong>
      </header>
      <div className="admin-table">
        {queue?.operations.length === 0 && <p>{t("admin.noOperations")}</p>}
        {queue?.operations.map((operation) => (
          <div key={operation.id}>
            <code>{operation.action}</code>
            <HealthBadge
              state={
                operation.result === "failed"
                  ? "blocked"
                  : operation.result === "requested"
                    ? "stale"
                    : "healthy"
              }
              t={t}
            />
            <time>{formatTime(operation.updatedAt, locale)}</time>
          </div>
        ))}
      </div>
    </section>
  );

  const renderOverview = () => (
    <>
      <section className="metric-strip">
        <article>
          <span>{t("latency")}</span>
          <strong>
            {metricSummary?.p95 === undefined ? "—" : `${Math.round(metricSummary.p95 * 1000)} ms`}
          </strong>
          <small>{t("admin.metrics.percentile")}</small>
        </article>
        <article>
          <span>{t("queue")}</span>
          <strong>
            {formatNumber((queue?.stats.ready ?? 0) + (queue?.stats.pending ?? 0), locale)}
          </strong>
          <small>{t("admin.queue.pending")}</small>
        </article>
        <article>
          <span>Sentry</span>
          <strong>
            {formatNumber(telemetry ? telemetry.traces + telemetry.profiles : undefined, locale)}
          </strong>
          <small>24h</small>
        </article>
        <article>
          <span>{t("admin.version")}</span>
          <strong>{status?.version ?? "—"}</strong>
          <small>{status?.goVersion ?? "—"}</small>
        </article>
      </section>
      <section className="admin-grid">
        <article className="admin-panel">
          <header>
            <strong>{t("services")}</strong>
            <span>
              {t("admin.checked")} · {formatTime(status?.checkedAt, locale)}
            </span>
          </header>
          <div className="service-table">
            {status?.components.map((component) => (
              <div key={component.name}>
                <span className={`health-dot health-dot-${component.status}`} />
                <strong>{component.name}</strong>
                <HealthBadge state={component.status} t={t} />
                <code>{healthDetail(t, component.detail)}</code>
              </div>
            ))}
          </div>
        </article>
        <QueueCards queue={queue} t={t} locale={locale} />
      </section>
      {renderOperations()}
    </>
  );

  const renderMetrics = () => (
    <section className="admin-panel">
      <header>
        <strong>{t("metrics")}</strong>
        <span>60 min</span>
      </header>
      {metrics.length === 0 ? (
        <p>{t("admin.metrics.empty")}</p>
      ) : (
        <div className="admin-data-table">
          <table>
            <caption>{t("metrics")}</caption>
            <thead>
              <tr>
                <th scope="col">{t("admin.metrics.name")}</th>
                <th scope="col">{t("admin.metrics.kind")}</th>
                <th scope="col">{t("admin.metrics.value")}</th>
                <th scope="col">{t("admin.metrics.percentile")}</th>
                <th scope="col">{t("admin.metrics.time")}</th>
              </tr>
            </thead>
            <tbody>
              {metrics.slice(0, 100).map((point) => (
                <tr key={`${point.bucket}-${point.name}-${JSON.stringify(point.labels)}`}>
                  <td>
                    <code>{point.name}</code>
                  </td>
                  <td>{point.kind}</td>
                  <td>
                    <strong>{formatNumber(point.value, locale)}</strong>
                  </td>
                  <td>{point.p95 === undefined ? "—" : point.p95.toFixed(3)}</td>
                  <td>
                    <time>{formatTime(point.bucket, locale)}</time>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );

  const renderLogs = () => (
    <>
      <section className="admin-toolbar">
        <label>
          <span>{t("logs")}</span>
          <select
            value={logLevel}
            onChange={(event) => setLogLevel(event.target.value as typeof logLevel)}
          >
            <option value="all">{t("admin.logs.all")}</option>
            <option value="debug">Debug</option>
            <option value="info">Info</option>
            <option value="warning">Warning</option>
            <option value="error">Error</option>
          </select>
        </label>
        <span>
          {t("admin.logs.dropped")}: {droppedLogs}
        </span>
        <Button variant="outline" onClick={() => setConfirmDebug(true)}>
          <TerminalSquare size={15} />
          {debug?.enabled ? t("admin.logs.disableDebug") : t("admin.logs.enableDebug")}
        </Button>
      </section>
      <section className="admin-panel">
        <div className="admin-log-table">
          {filteredLogs.length === 0 && <p>{t("admin.logs.empty")}</p>}
          {filteredLogs.map((entry) => (
            <div key={entry.id}>
              <time>{formatTime(entry.occurredAt, locale)}</time>
              <span className={`event-level ${entry.level === "warning" ? "warn" : entry.level}`}>
                {entry.level}
              </span>
              <code>{entry.event}</code>
              <span>
                {entry.service}/{entry.module}
              </span>
              <small>{entry.requestId ?? "—"}</small>
            </div>
          ))}
        </div>
      </section>
    </>
  );

  const renderErrors = () => (
    <section className="admin-panel">
      <header>
        <strong>{t("errors")}</strong>
        <span>{formatNumber(issues.length, locale)}</span>
      </header>
      <div className="admin-issue-list">
        {issues.length === 0 && <p>{t("admin.sentry.empty")}</p>}
        {issues.map((issue) => (
          <article key={issue.id}>
            <ShieldAlert size={18} />
            <div>
              <strong>{issue.title}</strong>
              <small>
                {issue.component} · {issue.environment} · {issue.eventCount}{" "}
                {t("admin.sentry.events")}
              </small>
            </div>
            <HealthBadge state={issue.status === "unresolved" ? "degraded" : "healthy"} t={t} />
            {issue.status === "unresolved" ? (
              <>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void setIssueStatus(issue, "resolved")}
                >
                  {t("admin.sentry.resolve")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void setIssueStatus(issue, "ignored")}
                >
                  {t("admin.sentry.ignore")}
                </Button>
              </>
            ) : (
              <Button
                size="sm"
                variant="outline"
                onClick={() => void setIssueStatus(issue, "unresolved")}
              >
                {t("admin.sentry.reopen")}
              </Button>
            )}
          </article>
        ))}
      </div>
    </section>
  );

  const renderTranslations = () => (
    <section className="admin-translation-grid">
      <label>
        <span>{t("admin.translations.key")}</span>
        <select
          value={selectedTranslationKey}
          onChange={(event) => chooseTranslation(event.target.value)}
        >
          {catalogKeys.map((key) => (
            <option key={key}>{key}</option>
          ))}
        </select>
      </label>
      <label>
        <span>{t("admin.translations.english")}</span>
        <textarea readOnly value={selectedEnglish?.value ?? ""} />
      </label>
      <label>
        <span>{t("admin.translations.spanish")}</span>
        <textarea
          value={spanishValue}
          onChange={(event) => {
            setSpanishValue(event.target.value);
            setCandidateValid(false);
            setTranslationNotice(null);
          }}
        />
      </label>
      <div className="admin-translation-actions">
        <Button variant="outline" onClick={() => setSpanishValue("")}>
          {t("admin.translations.reset")}
        </Button>
        <Button variant="outline" onClick={() => void validateCandidate()}>
          {t("admin.translations.validate")}
        </Button>
        <Button
          variant="primary"
          disabled={!candidateValid}
          onClick={() => void activateCandidate()}
        >
          {t("admin.translations.activate")}
        </Button>
      </div>
      {translationNotice && <p role="status">{t(translationNotice)}</p>}
    </section>
  );

  const renderCDN = () => (
    <section className="admin-stat-grid">
      <article>
        <HardDrive size={18} />
        <span>{t("admin.cdn.attachments")}</span>
        <strong>{formatBytes(cdn?.attachmentBytes)}</strong>
        <small>{formatNumber(cdn?.attachmentObjects, locale)}</small>
      </article>
      <article>
        <Database size={18} />
        <span>{t("admin.cdn.sentry")}</span>
        <strong>{formatBytes(cdn?.sentryBytes)}</strong>
        <small>{formatNumber(cdn?.sentryObjects, locale)}</small>
      </article>
      <article>
        <AlertTriangle size={18} />
        <span>{t("admin.cdn.missing")}</span>
        <strong>{formatNumber(cdn?.missingObjects, locale)}</strong>
      </article>
    </section>
  );

  const renderSection = () => {
    if (section === "overview") return renderOverview();
    if (section === "accounts") return renderQueueActions();
    if (section === "synchronization")
      return (
        <>
          <QueueCards queue={queue} t={t} locale={locale} />
          {renderQueueActions()}
          {renderOperations()}
        </>
      );
    if (section === "metrics") return renderMetrics();
    if (section === "logs") return renderLogs();
    if (section === "errors") return renderErrors();
    if (section === "translations") return renderTranslations();
    if (section === "cdn") return renderCDN();
    if (section === "backups")
      return (
        <>
          <section className="admin-stat-grid">
            <article>
              <Clock3 size={18} />
              <span>{t("admin.backups.status")}</span>
              <HealthBadge state={backups?.state} t={t} />
            </article>
            <article>
              <Database size={18} />
              <span>{t("admin.backups.repository")}</span>
              <strong>{backups?.runtime?.repositoryKind ?? "—"}</strong>
            </article>
            <article>
              <Clock3 size={18} />
              <span>{t("admin.backups.next")}</span>
              <strong>{formatTime(backups?.runtime?.nextRunAt, locale)}</strong>
            </article>
            <article>
              <CheckCircle2 size={18} />
              <span>{t("admin.backups.lastSuccess")}</span>
              <strong>{formatTime(backups?.lastSuccessAt, locale)}</strong>
            </article>
          </section>
          <section className="admin-panel">
            <header>
              <strong>{t("admin.backups.history")}</strong>
            </header>
            <div className="admin-list">
              {backups?.runs.length ? (
                backups.runs.map((run) => (
                  <article key={run.id}>
                    {run.state === "succeeded" ? (
                      <CheckCircle2 size={16} />
                    ) : (
                      <AlertTriangle size={16} />
                    )}
                    <strong>{t(`admin.backups.${run.state}` as TranslationKey)}</strong>
                    <span>{formatTime(run.startedAt, locale)}</span>
                    <span>{formatBytes(run.byteCount)}</span>
                    <small>{run.errorCode ?? run.snapshotId?.slice(0, 8) ?? "—"}</small>
                  </article>
                ))
              ) : (
                <p>{t("admin.backups.empty")}</p>
              )}
            </div>
          </section>
        </>
      );
    if (section === "updates")
      return (
        <section className="admin-placeholder">
          <Server size={24} />
          <p>{t("admin.placeholder.updates")}</p>
        </section>
      );
    return (
      <section className="admin-panel admin-settings">
        <header>
          <strong>{t("settings")}</strong>
          <Settings2 size={16} />
        </header>
        <h3>{t("admin.settings.retention")}</h3>
        <p>{t("admin.settings.logs")}</p>
        <p>{t("admin.settings.metrics")}</p>
        <p>{t("admin.settings.errors")}</p>
        <p>
          {t("admin.logs.debug")}:{" "}
          {debug?.enabled ? healthLabel(t, "degraded") : healthLabel(t, "healthy")}
        </p>
      </section>
    );
  };

  const activeLabel = t(sections.find((item) => item.id === section)?.key ?? "admin.overview");
  return (
    <div className="admin-shell">
      <header className="admin-header">
        <Brand />
        <div className="admin-header-actions">
          <label className="admin-mobile-navigation">
            <Languages size={15} />
            <select
              aria-label={t("admin")}
              value={section}
              onChange={(event) =>
                navigate(
                  event.target.value === "overview" ? "/admin" : `/admin/${event.target.value}`,
                )
              }
            >
              {sections.map((item) => (
                <option key={item.id} value={item.id}>
                  {t(item.key)}
                </option>
              ))}
            </select>
          </label>
          <Button variant="outline" aria-label={t("admin.refresh")} onClick={() => void refresh()}>
            <RefreshCw size={16} />
          </Button>
          <Button variant="outline" onClick={() => navigate("/")}>
            <ArrowLeft size={16} />
            {t("viewInbox")}
          </Button>
        </div>
      </header>
      <aside className="admin-sidebar">
        <h1>{t("admin")}</h1>
        <nav aria-label={t("admin")}>
          {sections.map((item) => (
            <button
              type="button"
              className={item.id === section ? "active" : ""}
              aria-current={item.id === section ? "page" : undefined}
              key={item.id}
              onClick={() => navigate(item.id === "overview" ? "/admin" : `/admin/${item.id}`)}
            >
              {t(item.key)}
            </button>
          ))}
        </nav>
      </aside>
      <main className="admin-content">
        <div className="admin-page-heading">
          <div>
            <span>{t("admin.system")}</span>
            <h1>{activeLabel}</h1>
          </div>
          {status && <HealthBadge state={status.state} t={t} />}
        </div>
        {loading && !status && (
          <p className="admin-loading" role="status">
            {t("admin.loading")}
          </p>
        )}
        {unavailable && (
          <p className="admin-error" role="alert">
            {t("admin.unavailable")}
          </p>
        )}
        {notice && (
          <p className="admin-notice" role="status">
            {t(notice)}
          </p>
        )}
        {renderSection()}
      </main>
      {confirmAccount && (
        <Confirmation
          title={t("admin.queue.confirmTitle")}
          body={t("admin.queue.confirmBody")}
          confirm={t("admin.queue.confirm")}
          cancel={t("admin.queue.cancel")}
          onConfirm={() => void retryAccount()}
          onCancel={() => setConfirmAccount(null)}
        />
      )}
      {confirmDebug && (
        <Confirmation
          title={t("admin.logs.confirmTitle")}
          body={t("admin.logs.confirmBody")}
          confirm={debug?.enabled ? t("admin.logs.disableDebug") : t("admin.logs.enableDebug")}
          cancel={t("admin.queue.cancel")}
          onConfirm={() => void toggleDebug()}
          onCancel={() => setConfirmDebug(false)}
        />
      )}
    </div>
  );
}
