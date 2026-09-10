import {
  Archive,
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  Download,
  Forward,
  ImageOff,
  Mail,
  MoreHorizontal,
  Paperclip,
  Reply,
  Tag,
  Trash2,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "./components/ui/button";
import type { ComposeContext } from "./composer";
import { isDesktopRuntime } from "./desktop-runtime";
import { type Locale, translate } from "./i18n";
import {
  type ConversationMessage,
  type ConversationPage,
  downloadMessageAttachment,
  loadConversationPage,
  type MailActionKind,
} from "./mailflow-api";
import { buildSafeMailDocument } from "./safe-mail";

function sender(message: ConversationMessage) {
  const address =
    message.addresses.find((item) => item.role === "from") ??
    message.addresses.find((item) => item.role === "sender");
  return {
    name: address?.displayName || address?.address || "—",
    address: address?.address || "",
  };
}

function recipients(message: ConversationMessage) {
  return message.addresses
    .filter((item) => item.role === "to" || item.role === "cc")
    .map((item) => item.displayName || item.address)
    .join(", ");
}

function readableBytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

function SafeMailFrame({ document, title }: { document: string; title: string }) {
  const desktop = isDesktopRuntime();
  const [desktopSource, setDesktopSource] = useState<string>();

  useEffect(() => {
    if (!desktop) {
      setDesktopSource(undefined);
      return;
    }
    const source = URL.createObjectURL(new Blob([document], { type: "text/html" }));
    setDesktopSource(source);
    return () => URL.revokeObjectURL(source);
  }, [desktop, document]);

  return (
    <iframe
      className="safe-mail-frame"
      title={title}
      sandbox="allow-popups"
      referrerPolicy="no-referrer"
      src={desktop ? desktopSource : undefined}
      srcDoc={desktop ? undefined : document}
    />
  );
}

function MessageAttachmentCard({
  attachment,
  locale,
  enabled,
}: {
  attachment: ConversationMessage["attachments"][number];
  locale: Locale;
  enabled: boolean;
}) {
  const t = (key: Parameters<typeof translate>[1]) => translate(locale, key);
  const [progress, setProgress] = useState<{ loaded: number; total: number } | null>(null);
  const [failed, setFailed] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const download = async () => {
    const current = new AbortController();
    controller.current = current;
    setFailed(false);
    setProgress({ loaded: 0, total: attachment.sizeBytes });
    try {
      const blob = await downloadMessageAttachment(
        attachment.id,
        (loaded, total) => setProgress({ loaded, total }),
        current.signal,
      );
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = attachment.filename || "attachment";
      link.click();
      URL.revokeObjectURL(url);
    } catch {
      if (!current.signal.aborted) setFailed(true);
    } finally {
      if (controller.current === current) controller.current = null;
      setProgress(null);
    }
  };
  return (
    <li className="attachment-card">
      <Paperclip size={18} />
      <span>
        <strong>{attachment.filename || t("attachment")}</strong>
        <small>
          {attachment.mediaType} · {readableBytes(attachment.sizeBytes)}
        </small>
        {progress && (
          <progress
            value={progress.loaded}
            max={Math.max(progress.total, 1)}
            aria-label={t("downloadAttachment")}
          />
        )}
        {failed && <small role="alert">{t("attachmentFailed")}</small>}
      </span>
      {controller.current ? (
        <Button
          size="icon"
          aria-label={t("cancelDownload")}
          onClick={() => controller.current?.abort()}
        >
          <X size={15} />
        </Button>
      ) : (
        <Button
          size="icon"
          aria-label={t("downloadAttachment")}
          onClick={() => void download()}
          disabled={!enabled}
        >
          <Download size={16} />
        </Button>
      )}
    </li>
  );
}

function MessageCard({
  message,
  expanded,
  allowRemoteImages,
  onToggle,
  onReply,
  onForward,
  locale,
  canCompose,
  canAttachments,
}: {
  message: ConversationMessage;
  expanded: boolean;
  allowRemoteImages: boolean;
  onToggle: () => void;
  onReply: () => void;
  onForward: () => void;
  locale: Locale;
  canCompose: boolean;
  canAttachments: boolean;
}) {
  const t = (key: Parameters<typeof translate>[1]) => translate(locale, key);
  const from = sender(message);
  const destination = recipients(message);
  return (
    <article className={expanded ? "mail-body-card expanded" : "mail-body-card collapsed"}>
      <button
        type="button"
        className="message-summary"
        onClick={onToggle}
        aria-expanded={expanded}
        aria-controls={`message-${message.id}`}
      >
        <span className="sender-avatar" aria-hidden="true">
          {from.name.slice(0, 1).toUpperCase()}
        </span>
        <span className="message-summary-copy">
          <strong>{from.name}</strong>
          <small>{message.subject || t("noSubject")}</small>
        </span>
        <time dateTime={message.sentAt}>
          {new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(
            new Date(message.sentAt),
          )}
        </time>
        {expanded ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
      </button>
      {expanded && (
        <div className="mail-message-content" id={`message-${message.id}`}>
          <div className="message-addresses">
            <span>{from.address}</span>
            {destination && (
              <span>
                {t("toRecipients")} {destination}
              </span>
            )}
          </div>
          {message.bodyHtml ? (
            <SafeMailFrame
              title={`${t("messageFrom")} ${from.name}`}
              document={buildSafeMailDocument(message.bodyHtml, allowRemoteImages)}
            />
          ) : (
            <pre className="plain-mail-body">{message.bodyText || t("emptyMessage")}</pre>
          )}
          {message.attachments.length > 0 && (
            <ul className="conversation-attachments" aria-label={t("attachments")}>
              {message.attachments.map((attachment) => (
                <MessageAttachmentCard
                  key={attachment.id}
                  attachment={attachment}
                  locale={locale}
                  enabled={canAttachments}
                />
              ))}
            </ul>
          )}
          {canCompose && (
            <div className="reply-actions">
              <Button variant="outline" onClick={onReply}>
                <Reply size={16} /> {t("reply")}
              </Button>
              <Button variant="outline" onClick={onForward}>
                <Forward size={16} /> {t("forward")}
              </Button>
            </div>
          )}
        </div>
      )}
    </article>
  );
}

export function ConversationView({
  accountId,
  threadId,
  locale,
  onBack,
  onCompose,
  onAction,
  canActions,
  canCompose,
  canAttachments,
  labelId,
}: {
  accountId: string;
  threadId: string;
  locale: Locale;
  onBack: () => void;
  onCompose: (context: ComposeContext) => void;
  onAction: (kind: MailActionKind, labelId?: string) => void;
  canActions: boolean;
  canCompose: boolean;
  canAttachments: boolean;
  labelId?: string;
}) {
  const t = (key: Parameters<typeof translate>[1]) => translate(locale, key);
  const [page, setPage] = useState<ConversationPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [allowRemoteImages, setAllowRemoteImages] = useState(false);

  const load = useCallback(
    async (cursor?: string, signal?: AbortSignal) => {
      setLoading(true);
      setFailed(false);
      try {
        const next = await loadConversationPage(accountId, threadId, cursor, signal);
        setPage((current) =>
          cursor && current ? { ...next, messages: [...current.messages, ...next.messages] } : next,
        );
        setExpanded((current) => {
          if (current.size > 0) return current;
          const latest = next.messages.at(-1);
          return latest ? new Set([latest.id]) : current;
        });
      } catch {
        if (!signal?.aborted) setFailed(true);
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [accountId, threadId],
  );

  useEffect(() => {
    const controller = new AbortController();
    setPage(null);
    setExpanded(new Set());
    setAllowRemoteImages(false);
    void load(undefined, controller.signal);
    return () => controller.abort();
  }, [load]);

  const subject = [...(page?.messages ?? [])].reverse().find((message) => message.subject)?.subject;
  const hasRemoteImages = useMemo(
    () =>
      page?.messages.some((message) => /data-mailflow-src|<img\b/i.test(message.bodyHtml)) ?? false,
    [page],
  );

  return (
    <section className="conversation" aria-labelledby="conversation-title">
      <div className="conversation-toolbar">
        <Button size="icon" aria-label={t("back")} onClick={onBack}>
          <ArrowLeft size={18} />
        </Button>
        {canActions && (
          <>
            <Button size="icon" aria-label={t("archive")} onClick={() => onAction("archive")}>
              <Archive size={17} />
            </Button>
            <Button size="icon" aria-label={t("delete")} onClick={() => onAction("move_to_trash")}>
              <Trash2 size={17} />
            </Button>
            <Button
              size="icon"
              aria-label={t("markUnread")}
              onClick={() => onAction("mark_unread")}
            >
              <Mail size={17} />
            </Button>
            {labelId && (
              <Button
                size="icon"
                aria-label={t("labels")}
                onClick={() => onAction("add_label", labelId)}
              >
                <Tag size={17} />
              </Button>
            )}
            <Button size="icon" aria-label={t("more")}>
              <MoreHorizontal size={18} />
            </Button>
          </>
        )}
      </div>
      <div className="conversation-heading" data-sentry-block>
        <h1 id="conversation-title">{subject || t("noSubject")}</h1>
        {hasRemoteImages && !allowRemoteImages && (
          <Button variant="outline" onClick={() => setAllowRemoteImages(true)}>
            <ImageOff size={16} /> {t("displayRemoteImages")}
          </Button>
        )}
      </div>
      <div className="conversation-messages" data-sentry-block>
        {loading && !page && (
          <div className="mail-state" role="status">
            {t("loadingConversation")}
          </div>
        )}
        {failed && !page && (
          <div className="mail-state" role="alert">
            <strong>{t("conversationFailed")}</strong>
            <Button variant="outline" onClick={() => void load()}>
              {t("tryAgain")}
            </Button>
          </div>
        )}
        {page?.messages.map((message) => (
          <MessageCard
            key={message.id}
            message={message}
            expanded={expanded.has(message.id)}
            allowRemoteImages={allowRemoteImages}
            onToggle={() =>
              setExpanded((current) => {
                const next = new Set(current);
                if (next.has(message.id)) next.delete(message.id);
                else next.add(message.id);
                return next;
              })
            }
            onReply={() => {
              const from = sender(message);
              const subject = /^re:/iu.test(message.subject)
                ? message.subject
                : `Re: ${message.subject}`;
              const quoted = message.bodyText
                .split("\n")
                .map((line) => `> ${line}`)
                .join("\n");
              onCompose({
                mode: "reply",
                sourceMessageId: message.id,
                to: from.address,
                subject,
                bodyText: `\n\nOn ${new Intl.DateTimeFormat(locale).format(new Date(message.sentAt))}, ${from.name} wrote:\n${quoted}`,
              });
            }}
            onForward={() => {
              const from = sender(message);
              const subject = /^fwd:/iu.test(message.subject)
                ? message.subject
                : `Fwd: ${message.subject}`;
              onCompose({
                mode: "forward",
                sourceMessageId: message.id,
                subject,
                bodyText: `\n\n---------- Forwarded message ----------\nFrom: ${from.name} <${from.address}>\nDate: ${new Intl.DateTimeFormat(locale).format(new Date(message.sentAt))}\nSubject: ${message.subject}\n\n${message.bodyText}`,
              });
            }}
            locale={locale}
            canCompose={canCompose}
            canAttachments={canAttachments}
          />
        ))}
        {page && page.messages.length === 0 && (
          <div className="mail-state" role="status">
            {t("emptyConversation")}
          </div>
        )}
        {page?.nextCursor && (
          <Button
            variant="outline"
            disabled={loading}
            onClick={() => void load(page.nextCursor ?? undefined)}
          >
            {loading ? t("loadingConversation") : t("loadMoreMessages")}
          </Button>
        )}
      </div>
    </section>
  );
}
