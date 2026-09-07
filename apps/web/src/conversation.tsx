import {
  Archive,
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  Forward,
  ImageOff,
  Mail,
  MoreHorizontal,
  Paperclip,
  Reply,
  Tag,
  Trash2,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Button } from "./components/ui/button";
import type { ComposeContext } from "./composer";
import { type Locale, translate } from "./i18n";
import {
  type ConversationMessage,
  type ConversationPage,
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

function MessageCard({
  message,
  expanded,
  allowRemoteImages,
  onToggle,
  onReply,
  onForward,
  locale,
}: {
  message: ConversationMessage;
  expanded: boolean;
  allowRemoteImages: boolean;
  onToggle: () => void;
  onReply: () => void;
  onForward: () => void;
  locale: Locale;
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
            <iframe
              className="safe-mail-frame"
              title={`${t("messageFrom")} ${from.name}`}
              sandbox="allow-popups"
              referrerPolicy="no-referrer"
              srcDoc={buildSafeMailDocument(message.bodyHtml, allowRemoteImages)}
            />
          ) : (
            <pre className="plain-mail-body">{message.bodyText || t("emptyMessage")}</pre>
          )}
          {message.attachments.length > 0 && (
            <ul className="conversation-attachments" aria-label={t("attachments")}>
              {message.attachments.map((attachment) => (
                <li className="attachment-card" key={attachment.id}>
                  <Paperclip size={18} />
                  <span>
                    <strong>{attachment.filename || t("attachment")}</strong>
                    <small>
                      {attachment.mediaType} · {readableBytes(attachment.sizeBytes)}
                    </small>
                  </span>
                </li>
              ))}
            </ul>
          )}
          <div className="reply-actions">
            <Button variant="outline" onClick={onReply}>
              <Reply size={16} /> {t("reply")}
            </Button>
            <Button variant="outline" onClick={onForward}>
              <Forward size={16} /> {t("forward")}
            </Button>
          </div>
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
}: {
  accountId: string;
  threadId: string;
  locale: Locale;
  onBack: () => void;
  onCompose: (context: ComposeContext) => void;
  onAction: (kind: MailActionKind) => void;
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
        <Button size="icon" aria-label={t("archive")} onClick={() => onAction("archive")}>
          <Archive size={17} />
        </Button>
        <Button size="icon" aria-label={t("delete")} onClick={() => onAction("move_to_trash")}>
          <Trash2 size={17} />
        </Button>
        <Button size="icon" aria-label={t("markUnread")} onClick={() => onAction("mark_unread")}>
          <Mail size={17} />
        </Button>
        <Button size="icon" aria-label={t("labels")}>
          <Tag size={17} />
        </Button>
        <Button size="icon" aria-label={t("more")}>
          <MoreHorizontal size={18} />
        </Button>
      </div>
      <div className="conversation-heading">
        <h1 id="conversation-title">{subject || t("noSubject")}</h1>
        {hasRemoteImages && !allowRemoteImages && (
          <Button variant="outline" onClick={() => setAllowRemoteImages(true)}>
            <ImageOff size={16} /> {t("displayRemoteImages")}
          </Button>
        )}
      </div>
      <div className="conversation-messages">
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
