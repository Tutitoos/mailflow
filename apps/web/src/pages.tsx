import { useVirtualizer } from "@tanstack/react-virtual";
import {
  Archive,
  ArrowLeft,
  Bell,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronsUpDown,
  CircleUserRound,
  FileText,
  Inbox,
  Info,
  Languages,
  Mail,
  MailOpen,
  Menu,
  Minus,
  MoreHorizontal,
  PanelRightClose,
  Paperclip,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Send,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Square,
  Star,
  Tag,
  Trash2,
  Users,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "./components/ui/button";
import { ConversationView } from "./conversation";
import { type Locale, type TranslationKey, translate } from "./i18n";
import {
  createMailActions,
  disconnectAccount,
  type InboxThread,
  loadGoogleAccounts,
  loadInboxPage,
  loadMailAccounts,
  loadMailNavigation,
  type MailAccount,
  type MailActionKind,
  type Mailbox,
  type MailCategory,
  type MailLabel,
  type SearchResult,
  searchMail,
  startGoogleConnection,
  subscribeMailEvents,
} from "./mailflow-api";
import { type SearchSyntaxError, searchSuggestions, validateSearchSyntax } from "./search-syntax";

type Translator = (key: TranslationKey) => string;

const inboxCategories: Array<{
  id: MailCategory;
  labelKey: TranslationKey;
  icon: typeof Inbox;
}> = [
  { id: "primary", labelKey: "category.primary", icon: Inbox },
  { id: "promotions", labelKey: "category.promotions", icon: Tag },
  { id: "social", labelKey: "category.social", icon: Users },
  { id: "notifications", labelKey: "category.updates", icon: Info },
  { id: "forums", labelKey: "category.forums", icon: Mail },
];

export function Brand() {
  return (
    <div className="brand">
      <div className="brand-mark" aria-hidden="true">
        <span />
        <span />
      </div>
      <span className="brand-name">Mailflow</span>
    </div>
  );
}

function Header({
  locale,
  onLocaleChange,
  query,
  onQueryChange,
  onSearch,
  searchInvalid,
  onMenu,
  syncLabel,
  connected,
  t,
}: {
  locale: Locale;
  onLocaleChange: () => void;
  query: string;
  onQueryChange: (value: string) => void;
  onSearch: (value: string) => void;
  searchInvalid: boolean;
  onMenu: () => void;
  syncLabel: string;
  connected: boolean;
  t: Translator;
}) {
  const navigate = useNavigate();
  const searchInput = useRef<HTMLInputElement>(null);
  const [showSearchHelp, setShowSearchHelp] = useState(false);
  useEffect(() => {
    const focusSearch = (event: KeyboardEvent) => {
      if (
        event.key === "/" &&
        !(event.target instanceof HTMLInputElement) &&
        !(event.target instanceof HTMLTextAreaElement)
      ) {
        event.preventDefault();
        searchInput.current?.focus();
      }
    };
    window.addEventListener("keydown", focusSearch);
    return () => window.removeEventListener("keydown", focusSearch);
  }, []);
  return (
    <header className="topbar">
      <div className="topbar-start">
        <Button size="icon" aria-label="Toggle navigation" onClick={onMenu}>
          <Menu size={19} />
        </Button>
        <Brand />
      </div>
      <search>
        <form
          className="search-box"
          onSubmit={(event) => {
            event.preventDefault();
            setShowSearchHelp(false);
            onSearch(query);
          }}
        >
          <Search size={18} aria-hidden="true" />
          <input
            ref={searchInput}
            type="search"
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                onQueryChange("");
                onSearch("");
              }
            }}
            placeholder={t("search")}
            aria-label={t("search")}
            aria-invalid={searchInvalid || undefined}
            aria-describedby={searchInvalid ? "search-error" : undefined}
            list="mailflow-search-operators"
          />
          <datalist id="mailflow-search-operators">
            {searchSuggestions.map((suggestion) => (
              <option key={suggestion} value={suggestion} />
            ))}
          </datalist>
          <kbd>/</kbd>
          <Button
            size="icon"
            aria-label={t("searchFilters")}
            type="button"
            aria-expanded={showSearchHelp}
            onClick={() => setShowSearchHelp((value) => !value)}
          >
            <SlidersHorizontal size={17} />
          </Button>
          {showSearchHelp && (
            <fieldset className="search-help">
              <legend>{t("searchFilters")}</legend>
              {searchSuggestions.map((suggestion) => (
                <button
                  key={suggestion}
                  type="button"
                  onClick={() => {
                    onQueryChange(`${query}${query.trim() ? " " : ""}${suggestion}`);
                    setShowSearchHelp(false);
                    searchInput.current?.focus();
                  }}
                >
                  {suggestion}
                </button>
              ))}
            </fieldset>
          )}
        </form>
      </search>
      <div className="topbar-actions">
        <span className={connected ? "sync-dot" : "offline-dot"} title={syncLabel} />
        <Button
          size="icon"
          aria-label={t("settings")}
          onClick={() => navigate("/settings/accounts")}
        >
          <Settings size={18} />
        </Button>
        <Button size="icon" aria-label="Notifications">
          <Bell size={18} />
        </Button>
        <Button className="locale-button" size="sm" onClick={onLocaleChange}>
          <Languages size={16} /> {locale.toUpperCase()}
        </Button>
        <button className="avatar" type="button" aria-label="Account menu">
          G
        </button>
      </div>
    </header>
  );
}

const mailboxItems = [
  ["inbox", Inbox, "38"],
  ["starred", Star, ""],
  ["important", Tag, ""],
  ["allMail", MailOpen, ""],
  ["drafts", FileText, "3"],
  ["sent", Send, ""],
] as const;

function Sidebar({
  collapsed,
  onCompose,
  accounts,
  activeAccountId,
  onAccountChange,
  mailboxes,
  labels,
  t,
}: {
  collapsed: boolean;
  onCompose: () => void;
  accounts: MailAccount[];
  activeAccountId: string | null;
  onAccountChange: (accountId: string) => void;
  mailboxes: Mailbox[];
  labels: MailLabel[];
  t: Translator;
}) {
  const mailboxByRole = new Map(mailboxes.map((mailbox) => [mailbox.role, mailbox]));
  return (
    <aside className={collapsed ? "sidebar collapsed" : "sidebar"}>
      <Button className="compose-button" variant="primary" onClick={onCompose}>
        <Pencil size={18} />
        <span>{t("compose")}</span>
      </Button>
      <nav aria-label="Mailboxes" className="nav-list">
        {mailboxItems.map(([key, Icon], index) => {
          const role = key === "allMail" ? "all" : key;
          const mailbox = mailboxByRole.get(role as Mailbox["role"]);
          const count = mailbox?.unreadCount ?? 0;
          return (
            <button
              className={index === 0 ? "nav-item active" : "nav-item"}
              type="button"
              key={key}
            >
              <Icon size={17} />
              <span>{mailbox?.localName || mailbox?.remoteName || t(key)}</span>
              {count > 0 && <strong>{count}</strong>}
            </button>
          );
        })}
        <button className="nav-item" type="button">
          <ChevronDown size={17} />
          <span>{t("more")}</span>
        </button>
      </nav>
      <div className="sidebar-section-title">
        <span>{t("labels")}</span>
        <Plus size={16} />
      </div>
      <nav className="nav-list labels" aria-label={t("labels")}>
        {labels
          .filter((label) => label.kind === "user")
          .slice(0, 12)
          .map((label) => (
            <button className="nav-item" type="button" key={label.id}>
              <span className="label-dot" style={{ background: label.color || undefined }} />
              <span>{label.localName || label.remoteName}</span>
              {label.unreadCount > 0 && <strong>{label.unreadCount}</strong>}
            </button>
          ))}
      </nav>
      <div className="sidebar-section-title account-title">
        <span>{t("accounts")}</span>
      </div>
      <nav className="nav-list accounts" aria-label={t("accounts")}>
        {accounts.map((account) => (
          <button
            className={account.id === activeAccountId ? "nav-item active" : "nav-item"}
            type="button"
            key={account.id}
            onClick={() => onAccountChange(account.id)}
          >
            <span className="provider-icon">{account.provider.slice(0, 1).toUpperCase()}</span>
            <span>{account.displayName}</span>
            <span className={account.syncState === "error" ? "offline-dot" : "online-dot"} />
          </button>
        ))}
      </nav>
    </aside>
  );
}

function MailToolbar({
  allSelected,
  onSelectAll,
  onRefresh,
  onPrevious,
  onNext,
  canPrevious,
  canNext,
  range,
  selectedCount,
  onArchive,
  onTrash,
  onUnread,
  onLabel,
  t,
}: {
  allSelected: boolean;
  onSelectAll: () => void;
  onRefresh: () => void;
  onPrevious: () => void;
  onNext: () => void;
  canPrevious: boolean;
  canNext: boolean;
  range: string;
  selectedCount: number;
  onArchive: () => void;
  onTrash: () => void;
  onUnread: () => void;
  onLabel: () => void;
  t: Translator;
}) {
  return (
    <div className="mail-toolbar">
      <div className="toolbar-group">
        <button
          className={allSelected ? "select-box checked" : "select-box"}
          onClick={onSelectAll}
          type="button"
          aria-label={t("selectAll")}
        >
          {allSelected ? "✓" : ""}
        </button>
        <ChevronDown size={14} />
        <Button size="icon" aria-label={t("refresh")} onClick={onRefresh}>
          <RefreshCw size={17} />
        </Button>
        {selectedCount > 0 && (
          <>
            <Button size="icon" aria-label={t("archive")} onClick={onArchive}>
              <Archive size={17} />
            </Button>
            <Button size="icon" aria-label={t("delete")} onClick={onTrash}>
              <Trash2 size={17} />
            </Button>
            <Button size="icon" aria-label={t("markUnread")} onClick={onUnread}>
              <Mail size={17} />
            </Button>
            <Button size="icon" aria-label={t("applyLabel")} onClick={onLabel}>
              <Tag size={17} />
            </Button>
          </>
        )}
        <Button size="icon" aria-label="More actions">
          <MoreHorizontal size={18} />
        </Button>
      </div>
      <div className="toolbar-group range-controls">
        <span>{range}</span>
        <Button
          size="icon"
          aria-label={t("previousPage")}
          onClick={onPrevious}
          disabled={!canPrevious}
        >
          <ChevronLeft size={17} />
        </Button>
        <Button size="icon" aria-label={t("nextPage")} onClick={onNext} disabled={!canNext}>
          <ChevronRight size={17} />
        </Button>
      </div>
    </div>
  );
}

function CategoryTabs({
  active,
  onChange,
  labels,
  t,
}: {
  active: MailCategory;
  onChange: (category: MailCategory) => void;
  labels: MailLabel[];
  t: Translator;
}) {
  return (
    <div className="category-tabs" role="tablist" aria-label={t("inboxCategories")}>
      {inboxCategories.map((category) => {
        const Icon = category.icon;
        const count = labels.find((label) => label.category === category.id)?.unreadCount ?? 0;
        return (
          <button
            type="button"
            role="tab"
            aria-selected={active === category.id}
            className={active === category.id ? "category-tab active" : "category-tab"}
            key={category.id}
            onClick={() => onChange(category.id)}
          >
            <Icon size={17} />
            <span>{t(category.labelKey as TranslationKey)}</span>
            {count > 0 && <strong>{count}</strong>}
          </button>
        );
      })}
    </div>
  );
}

function MessageRow({
  message,
  selected,
  starred,
  onSelect,
  onStar,
  onOpen,
  onArchive,
  onTrash,
  onUnread,
  onImportant,
  openLabel,
}: {
  message: InboxThread;
  selected: boolean;
  starred: boolean;
  onSelect: () => void;
  onStar: () => void;
  onOpen: () => void;
  onArchive: () => void;
  onTrash: () => void;
  onUnread: () => void;
  onImportant: () => void;
  openLabel: string;
}) {
  return (
    <article
      className={`message-row${!message.isRead ? " unread" : ""}${selected ? " selected" : ""}`}
    >
      <button
        className={selected ? "select-box checked" : "select-box"}
        type="button"
        onClick={onSelect}
        aria-label={`Select ${message.subject || message.senderName}`}
      >
        {selected ? "✓" : ""}
      </button>
      <button
        className={starred ? "row-icon starred" : "row-icon"}
        type="button"
        onClick={onStar}
        aria-label={`Star ${message.subject || message.senderName}`}
      >
        <Star size={16} fill={starred ? "currentColor" : "none"} />
      </button>
      <button
        className="row-icon important"
        type="button"
        aria-label={`Mark ${message.subject || message.senderName} important`}
        onClick={onImportant}
      >
        <ChevronsUpDown size={16} />
      </button>
      <button className="message-content" type="button" onClick={onOpen} aria-label={openLabel}>
        <span className="sender">{message.senderName || message.senderAddress}</span>
        <span className="subject-line">
          <strong>{message.subject}</strong>
          <span> — {message.preview}</span>
        </span>
        {message.attachmentCount > 0 && (
          <span className="attachment-chip">
            <Paperclip size={13} />
            {message.attachmentCount}
          </span>
        )}
        <span className="provider-badge" title={message.senderAddress}>
          {message.messageCount}
        </span>
        <time dateTime={message.lastMessageAt}>
          {new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" }).format(
            new Date(message.lastMessageAt),
          )}
        </time>
      </button>
      <div className="quick-actions">
        <Button size="icon" aria-label="Archive" onClick={onArchive}>
          <Archive size={16} />
        </Button>
        <Button size="icon" aria-label="Delete" onClick={onTrash}>
          <Trash2 size={16} />
        </Button>
        <Button size="icon" aria-label="Mark unread" onClick={onUnread}>
          <Mail size={16} />
        </Button>
      </div>
    </article>
  );
}

function ComposePanel({
  maximized,
  onMaximize,
  onClose,
  t,
}: {
  maximized: boolean;
  onMaximize: () => void;
  onClose: () => void;
  t: Translator;
}) {
  return (
    <section
      className={maximized ? "compose-panel maximized" : "compose-panel"}
      aria-label={t("newMessage")}
    >
      <header>
        <strong>{t("newMessage")}</strong>
        <div>
          <Button size="icon" aria-label={t("minimize")}>
            <Minus size={15} />
          </Button>
          <Button size="icon" aria-label={t("maximize")} onClick={onMaximize}>
            <Square size={13} />
          </Button>
          <Button size="icon" aria-label={t("close")} onClick={onClose}>
            <X size={16} />
          </Button>
        </div>
      </header>
      <label className="compose-field">
        <span>To</span>
        <input aria-label={t("recipients")} />
        <button type="button">Cc Bcc</button>
      </label>
      <label className="compose-field">
        <input aria-label={t("subject")} placeholder={t("subject")} />
      </label>
      <textarea aria-label={t("message")} placeholder={t("message")} />
      <footer>
        <Button variant="primary">
          <Send size={16} />
          {t("send")}
        </Button>
        <div>
          <Button size="icon" aria-label="Attach file">
            <Paperclip size={17} />
          </Button>
          <Button size="icon" aria-label="Discard draft">
            <Trash2 size={17} />
          </Button>
        </div>
      </footer>
    </section>
  );
}

function ContextRail({ t }: { t: Translator }) {
  return (
    <aside className="context-rail">
      <Button size="icon" aria-label={t("details")}>
        <Info size={18} />
      </Button>
      <Button size="icon" aria-label={t("attachments")}>
        <Paperclip size={18} />
      </Button>
      <Button size="icon" aria-label={t("account")}>
        <CircleUserRound size={18} />
      </Button>
      <div className="rail-spacer" />
      <Button size="icon" aria-label="Close rail">
        <PanelRightClose size={18} />
      </Button>
    </aside>
  );
}

function VirtualMessageList({
  messages,
  selected,
  starred,
  onSelect,
  onStar,
  onOpen,
  onAction,
  t,
}: {
  messages: InboxThread[];
  selected: Set<string>;
  starred: Set<string>;
  onSelect: (id: string) => void;
  onStar: (id: string) => void;
  onOpen: (id: string) => void;
  onAction: (kind: MailActionKind, ids: string[]) => void;
  t: Translator;
}) {
  const parentRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: messages.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 48,
    overscan: 12,
  });

  return (
    <section className="message-list" aria-live="polite" ref={parentRef}>
      <div className="virtual-message-list" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((virtualRow) => {
          const message = messages[virtualRow.index];
          if (!message) return null;
          return (
            <div
              className="virtual-message-row"
              key={message.id}
              ref={virtualizer.measureElement}
              data-index={virtualRow.index}
              style={{ transform: `translateY(${virtualRow.start}px)` }}
            >
              <MessageRow
                message={message}
                selected={selected.has(message.id)}
                starred={starred.has(message.id) || message.isStarred}
                onSelect={() => onSelect(message.id)}
                onOpen={() => onOpen(message.id)}
                onStar={() => onStar(message.id)}
                onArchive={() => onAction("archive", [message.id])}
                onTrash={() => onAction("move_to_trash", [message.id])}
                onUnread={() => onAction("mark_unread", [message.id])}
                onImportant={() =>
                  onAction(message.isImportant ? "mark_unimportant" : "mark_important", [
                    message.id,
                  ])
                }
                openLabel={`${t("openMessage")} ${message.subject || message.senderName}`}
              />
            </div>
          );
        })}
      </div>
    </section>
  );
}

export function MailPage({ initialLocale = "en" }: { initialLocale?: Locale }) {
  const navigate = useNavigate();
  const initialQuery = new URLSearchParams(window.location.search).get("q") ?? "";
  const [locale, setLocale] = useState<Locale>(initialLocale);
  const [query, setQuery] = useState(initialQuery);
  const [submittedSearch, setSubmittedSearch] = useState(
    validateSearchSyntax(initialQuery) === null ? initialQuery : "",
  );
  const [searchError, setSearchError] = useState<SearchSyntaxError | "request_failed" | null>(
    initialQuery && validateSearchSyntax(initialQuery) ? validateSearchSyntax(initialQuery) : null,
  );
  const [searchResults, setSearchResults] = useState<SearchResult[]>([]);
  const [searchNextCursor, setSearchNextCursor] = useState<string | null>(null);
  const [searchCursor, setSearchCursor] = useState<string | undefined>();
  const [searchCursorHistory, setSearchCursorHistory] = useState<string[]>([]);
  const [searchLoadState, setSearchLoadState] = useState<"loading" | "ready" | "error">("ready");
  const [category, setCategory] = useState<MailCategory>("primary");
  const [accounts, setAccounts] = useState<MailAccount[]>([]);
  const [activeAccountId, setActiveAccountId] = useState<string | null>(null);
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [labels, setLabels] = useState<MailLabel[]>([]);
  const [navigationPartial, setNavigationPartial] = useState(false);
  const [threads, setThreads] = useState<InboxThread[]>([]);
  const [activeThreadId, setActiveThreadId] = useState<string | null>(null);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [pageCursor, setPageCursor] = useState<string | undefined>();
  const [cursorHistory, setCursorHistory] = useState<string[]>([]);
  const [loadState, setLoadState] = useState<"loading" | "ready" | "error" | "resync">("loading");
  const [refreshRevision, setRefreshRevision] = useState(0);
  const [online, setOnline] = useState(() => navigator.onLine);
  const [eventsConnected, setEventsConnected] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [starred, setStarred] = useState<Set<string>>(new Set());
  const [actionNotice, setActionNotice] = useState<"pending" | "failed" | "partial" | null>(null);
  const actionKeys = useRef(new Map<string, string>());
  const [sidebarCollapsed, setSidebarCollapsed] = useState(
    () => window.matchMedia("(max-width: 900px)").matches,
  );
  const [composeOpen, setComposeOpen] = useState(false);
  const [composeMaximized, setComposeMaximized] = useState(false);
  const t: Translator = (key) => translate(locale, key);

  useEffect(() => {
    const controller = new AbortController();
    setLoadState("loading");
    void loadMailAccounts(controller.signal)
      .then(({ items }) => {
        const active = items.filter((account) => !account.disabledAt);
        setAccounts(active);
        setActiveAccountId((current) =>
          current && active.some((account) => account.id === current)
            ? current
            : (active[0]?.id ?? null),
        );
        if (active.length === 0) setLoadState("ready");
      })
      .catch(() => {
        if (!controller.signal.aborted) setLoadState("error");
      });
    return () => controller.abort();
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies: refreshRevision intentionally retries navigation.
  useEffect(() => {
    if (!activeAccountId) {
      setMailboxes([]);
      setLabels([]);
      setThreads([]);
      return;
    }
    const controller = new AbortController();
    setNavigationPartial(false);
    void loadMailNavigation(activeAccountId, controller.signal)
      .then((navigation) => {
        setMailboxes(navigation.mailboxes);
        setLabels(navigation.labels);
      })
      .catch(() => {
        if (!controller.signal.aborted) setNavigationPartial(true);
      });
    return () => controller.abort();
  }, [activeAccountId, refreshRevision]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: refreshRevision intentionally reloads the current cursor.
  useEffect(() => {
    if (!activeAccountId || !online) return;
    const controller = new AbortController();
    setLoadState("loading");
    void loadInboxPage(activeAccountId, category, pageCursor, controller.signal)
      .then((page) => {
        setThreads(page.items);
        setNextCursor(page.nextCursor);
        setStarred((current) => {
          const next = new Set(current);
          for (const item of page.items) if (item.isStarred) next.add(item.id);
          return next;
        });
        setLoadState("ready");
      })
      .catch(() => {
        if (!controller.signal.aborted) setLoadState("error");
      });
    return () => controller.abort();
  }, [activeAccountId, category, online, pageCursor, refreshRevision]);

  useEffect(() => {
    if (!activeAccountId || !online || !submittedSearch) return;
    const controller = new AbortController();
    setSearchLoadState("loading");
    void searchMail(activeAccountId, submittedSearch, searchCursor, controller.signal)
      .then((page) => {
        setSearchResults(page.items);
        setSearchNextCursor(page.nextCursor);
        setSearchLoadState("ready");
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setSearchError("request_failed");
          setSearchLoadState("error");
        }
      });
    return () => controller.abort();
  }, [activeAccountId, online, searchCursor, submittedSearch]);

  useEffect(() => {
    const becameOnline = () => {
      setOnline(true);
      setRefreshRevision((current) => current + 1);
    };
    const becameOffline = () => setOnline(false);
    window.addEventListener("online", becameOnline);
    window.addEventListener("offline", becameOffline);
    return () => {
      window.removeEventListener("online", becameOnline);
      window.removeEventListener("offline", becameOffline);
    };
  }, []);

  useEffect(() => {
    if (!activeAccountId || !online) return;
    const controller = new AbortController();
    void subscribeMailEvents(
      (event) => {
        const eventAccount = event.payload.accountId;
        if (typeof eventAccount === "string" && eventAccount !== activeAccountId) return;
        if (event.type === "system.resync_required") setLoadState("resync");
        if (event.type === "mail.changed" || event.type === "sync.progress") {
          setRefreshRevision((current) => current + 1);
        }
      },
      setEventsConnected,
      controller.signal,
    ).catch(() => setEventsConnected(false));
    return () => controller.abort();
  }, [activeAccountId, online]);

  const searchThreads = useMemo(() => {
    const unique = new Map<string, InboxThread>();
    for (const result of searchResults) {
      if (!unique.has(result.threadId)) {
        unique.set(result.threadId, {
          id: result.threadId,
          accountId: result.accountId,
          senderName: result.senderName,
          senderAddress: result.senderAddress,
          subject: result.subject,
          preview: result.preview,
          lastMessageAt: result.sentAt,
          isRead: result.isRead,
          isStarred: result.isStarred,
          isImportant: result.isImportant,
          category: "primary",
          messageCount: 1,
          attachmentCount: result.attachmentCount,
        });
      }
    }
    return [...unique.values()];
  }, [searchResults]);
  const searching = submittedSearch !== "";
  const displayedThreads = searching ? searchThreads : threads;
  const activeLoadState = searching ? searchLoadState : loadState;

  const submitSearch = (value: string) => {
    if (!value.trim()) {
      setSubmittedSearch("");
      setSearchError(null);
      setSearchResults([]);
      setSearchCursor(undefined);
      setSearchCursorHistory([]);
      window.history.replaceState(null, "", window.location.pathname);
      return;
    }
    const validation = validateSearchSyntax(value);
    if (validation) {
      setSearchError(validation);
      return;
    }
    const normalized = value.trim();
    setSearchError(null);
    setSearchCursor(undefined);
    setSearchCursorHistory([]);
    setSubmittedSearch(normalized);
    const url = new URL(window.location.href);
    url.searchParams.set("q", normalized);
    window.history.replaceState(null, "", `${url.pathname}${url.search}`);
  };

  const searchErrorMessage =
    searchError === "empty_query"
      ? t("searchEmpty")
      : searchError === "query_too_long"
        ? t("searchTooLong")
        : searchError === "unclosed_quote"
          ? t("searchUnclosedQuote")
          : searchError === "unsupported_operator"
            ? t("searchUnsupportedOperator")
            : searchError === "missing_value"
              ? t("searchMissingValue")
              : searchError === "invalid_value"
                ? t("searchInvalidValue")
                : searchError === "duplicate_operator"
                  ? t("searchDuplicateOperator")
                  : searchError === "invalid_range"
                    ? t("searchInvalidRange")
                    : searchError === "request_failed"
                      ? t("searchFailed")
                      : "";

  const runAction = async (kind: MailActionKind, targetIds: string[], labelId?: string) => {
    if (!activeAccountId || targetIds.length === 0) return;
    if (!online) {
      setActionNotice("failed");
      return;
    }
    const signature = `${kind}:${[...targetIds].sort().join(",")}:${labelId ?? ""}`;
    const key = actionKeys.current.get(signature) ?? `mailflow-${crypto.randomUUID()}`;
    actionKeys.current.set(signature, key);
    const previousThreads = threads;
    const previousSearch = searchResults;
    const previousStarred = new Set(starred);
    const targets = new Set(targetIds);
    const updateThread = (item: InboxThread) => ({
      ...item,
      isRead: kind === "mark_read" ? true : kind === "mark_unread" ? false : item.isRead,
      isImportant:
        kind === "mark_important" ? true : kind === "mark_unimportant" ? false : item.isImportant,
    });
    const remove = kind === "archive" || kind === "move_to_trash";
    setThreads((current) =>
      current
        .filter((item) => !remove || !targets.has(item.id))
        .map((item) => (targets.has(item.id) ? updateThread(item) : item)),
    );
    setSearchResults((current) =>
      current
        .filter((item) => !remove || !targets.has(item.threadId))
        .map((item) =>
          targets.has(item.threadId)
            ? {
                ...item,
                isRead: kind === "mark_read" ? true : kind === "mark_unread" ? false : item.isRead,
                isImportant:
                  kind === "mark_important"
                    ? true
                    : kind === "mark_unimportant"
                      ? false
                      : item.isImportant,
              }
            : item,
        ),
    );
    if (kind === "star" || kind === "unstar") {
      setStarred((current) => {
        const next = new Set(current);
        for (const id of targetIds) kind === "star" ? next.add(id) : next.delete(id);
        return next;
      });
    }
    setSelected((current) => new Set([...current].filter((id) => !targets.has(id))));
    setActionNotice("pending");
    try {
      const result = await createMailActions(activeAccountId, kind, targetIds, key, labelId);
      setActionNotice(result.partial ? "partial" : null);
      if (result.partial) setRefreshRevision((current) => current + 1);
    } catch {
      setThreads(previousThreads);
      setSearchResults(previousSearch);
      setStarred(previousStarred);
      setActionNotice("failed");
    } finally {
      actionKeys.current.delete(signature);
    }
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: keyboard bindings must follow the current optimistic action closure.
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if (
        event.target instanceof HTMLInputElement ||
        event.target instanceof HTMLTextAreaElement ||
        selected.size === 0
      )
        return;
      if (event.key.toLowerCase() === "e") {
        event.preventDefault();
        void runAction("archive", [...selected]);
      } else if (event.key === "#") {
        event.preventDefault();
        void runAction("move_to_trash", [...selected]);
      }
    };
    window.addEventListener("keydown", shortcut);
    return () => window.removeEventListener("keydown", shortcut);
  }, [selected, activeAccountId, online, threads, searchResults, starred]);

  const toggleSelection = (id: string) => {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  return (
    <div className="app-shell">
      <Header
        locale={locale}
        onLocaleChange={() => setLocale(locale === "en" ? "es" : "en")}
        query={query}
        onQueryChange={setQuery}
        onSearch={submitSearch}
        searchInvalid={Boolean(searchError)}
        onMenu={() => setSidebarCollapsed((value) => !value)}
        syncLabel={!online ? t("offline") : eventsConnected ? t("liveUpdates") : t("syncing")}
        connected={online && eventsConnected}
        t={t}
      />
      <div className="app-body">
        <Sidebar
          collapsed={sidebarCollapsed}
          onCompose={() => setComposeOpen(true)}
          accounts={accounts}
          activeAccountId={activeAccountId}
          onAccountChange={(accountId) => {
            setPageCursor(undefined);
            setCursorHistory([]);
            setActiveThreadId(null);
            setActiveAccountId(accountId);
          }}
          mailboxes={mailboxes}
          labels={labels}
          t={t}
        />
        <main className="mail-surface">
          {activeThreadId && activeAccountId ? (
            <ConversationView
              accountId={activeAccountId}
              threadId={activeThreadId}
              locale={locale}
              onBack={() => setActiveThreadId(null)}
              onCompose={() => setComposeOpen(true)}
              onAction={(kind) => {
                void runAction(kind, [activeThreadId]);
                if (kind === "archive" || kind === "move_to_trash") setActiveThreadId(null);
              }}
            />
          ) : (
            <>
              <MailToolbar
                allSelected={
                  displayedThreads.length > 0 &&
                  displayedThreads.every((message) => selected.has(message.id))
                }
                onSelectAll={() =>
                  setSelected(
                    displayedThreads.every((message) => selected.has(message.id))
                      ? new Set()
                      : new Set(displayedThreads.map((message) => message.id)),
                  )
                }
                onRefresh={() => setRefreshRevision((current) => current + 1)}
                onPrevious={() => {
                  if (searching) {
                    const previous = searchCursorHistory.at(-1);
                    setSearchCursorHistory((current) => current.slice(0, -1));
                    setSearchCursor(previous || undefined);
                  } else {
                    const previous = cursorHistory.at(-1);
                    setCursorHistory((current) => current.slice(0, -1));
                    setPageCursor(previous || undefined);
                  }
                }}
                onNext={() => {
                  const next = searching ? searchNextCursor : nextCursor;
                  if (!next) return;
                  if (searching) {
                    setSearchCursorHistory((current) => [...current, searchCursor ?? ""]);
                    setSearchCursor(next);
                  } else {
                    setCursorHistory((current) => [...current, pageCursor ?? ""]);
                    setPageCursor(next);
                  }
                }}
                canPrevious={(searching ? searchCursorHistory : cursorHistory).length > 0}
                canNext={Boolean(searching ? searchNextCursor : nextCursor)}
                range={`${(searching ? searchCursorHistory : cursorHistory).length * 50 + (displayedThreads.length ? 1 : 0)}–${
                  (searching ? searchCursorHistory : cursorHistory).length * 50 +
                  displayedThreads.length
                }`}
                selectedCount={selected.size}
                onArchive={() => void runAction("archive", [...selected])}
                onTrash={() => void runAction("move_to_trash", [...selected])}
                onUnread={() => void runAction("mark_unread", [...selected])}
                onLabel={() => {
                  const label = labels.find((item) => item.kind === "user");
                  if (label) void runAction("add_label", [...selected], label.id);
                }}
                t={t}
              />
              {!searching && (
                <CategoryTabs
                  active={category}
                  onChange={(nextCategory) => {
                    setPageCursor(undefined);
                    setCursorHistory([]);
                    setActiveThreadId(null);
                    setCategory(nextCategory);
                  }}
                  labels={labels}
                  t={t}
                />
              )}
              {searching && <div className="search-results-heading">{t("searchResults")}</div>}
              {searchErrorMessage && (
                <div id="search-error" className="search-error" role="alert">
                  {searchErrorMessage}
                </div>
              )}
              {actionNotice && (
                <div
                  className={actionNotice === "pending" ? "action-notice" : "action-notice error"}
                  role="status"
                >
                  {actionNotice === "pending"
                    ? t("actionPending")
                    : actionNotice === "partial"
                      ? t("actionPartial")
                      : t("actionFailed")}
                </div>
              )}
              {navigationPartial && (
                <div className="partial-banner" role="status">
                  {t("navigationPartial")}
                </div>
              )}
              {!online ? (
                <div className="mail-state" role="status">
                  <Inbox size={30} />
                  <strong>{t("offline")}</strong>
                  <span>{t("offlineDescription")}</span>
                </div>
              ) : activeLoadState === "loading" ? (
                <div className="mail-state" role="status">
                  <span className="loading-spinner" />
                  <strong>{searching ? t("searchResults") : t("loadingInbox")}</strong>
                </div>
              ) : activeLoadState === "error" ? (
                <div className="mail-state" role="alert">
                  <Inbox size={30} />
                  <strong>{searching ? t("searchFailed") : t("inboxFailed")}</strong>
                  <Button
                    variant="outline"
                    onClick={() => setRefreshRevision((current) => current + 1)}
                  >
                    {t("tryAgain")}
                  </Button>
                </div>
              ) : loadState === "resync" ? (
                <div className="mail-state" role="alert">
                  <RefreshCw size={30} />
                  <strong>{t("resyncRequired")}</strong>
                  <span>{t("resyncDescription")}</span>
                  <Button
                    variant="outline"
                    onClick={() => setRefreshRevision((current) => current + 1)}
                  >
                    {t("refresh")}
                  </Button>
                </div>
              ) : accounts.length === 0 ? (
                <div className="mail-state" role="status">
                  <Inbox size={30} />
                  <strong>{t("noConnectedAccounts")}</strong>
                  <Button variant="outline" onClick={() => navigate("/settings/accounts")}>
                    {t("connectGoogle")}
                  </Button>
                </div>
              ) : displayedThreads.length ? (
                <VirtualMessageList
                  messages={displayedThreads}
                  selected={selected}
                  starred={starred}
                  onSelect={toggleSelection}
                  onOpen={setActiveThreadId}
                  onAction={(kind, ids) => void runAction(kind, ids)}
                  t={t}
                  onStar={(id) => void runAction(starred.has(id) ? "unstar" : "star", [id])}
                />
              ) : (
                <div className="empty-state" role="status">
                  <Inbox size={30} />
                  <strong>{searching ? t("searchNoResults") : t("noMessages")}</strong>
                </div>
              )}
            </>
          )}
        </main>
        <ContextRail t={t} />
      </div>
      {composeOpen && (
        <ComposePanel
          maximized={composeMaximized}
          onMaximize={() => setComposeMaximized((value) => !value)}
          onClose={() => {
            setComposeOpen(false);
            setComposeMaximized(false);
          }}
          t={t}
        />
      )}
    </div>
  );
}

const serviceRows = [
  ["API", "Healthy", "12 ms"],
  ["Worker", "Healthy", "0 queued"],
  ["PostgreSQL", "Healthy", "6 connections"],
  ["Redis", "Healthy", "1.4 MB"],
  ["CDN", "Healthy", "284 MB"],
];

export function AdminPage() {
  const navigate = useNavigate();
  return (
    <div className="admin-shell">
      <header className="admin-header">
        <Brand />
        <Button variant="outline" onClick={() => navigate("/")}>
          <ArrowLeft size={16} />
          Inbox
        </Button>
      </header>
      <aside className="admin-sidebar">
        <h1>Admin</h1>
        {[
          "Status",
          "Accounts",
          "Synchronization",
          "Metrics",
          "Logs",
          "Errors",
          "Translations",
          "CDN",
          "Backups",
          "Updates",
          "Settings",
        ].map((item, index) => (
          <button type="button" className={index === 0 ? "active" : ""} key={item}>
            {item}
          </button>
        ))}
      </aside>
      <main className="admin-content">
        <div className="admin-title">
          <div>
            <span>System</span>
            <h2>Operational status</h2>
          </div>
          <div className="status-badge">
            <ShieldCheck size={15} />
            All systems operational
          </div>
        </div>
        <section className="metric-strip">
          <article>
            <span>Messages processed</span>
            <strong>18,420</strong>
            <small>+12.4% this week</small>
          </article>
          <article>
            <span>API latency</span>
            <strong>42 ms</strong>
            <small>p95 · last 24 hours</small>
          </article>
          <article>
            <span>Error rate</span>
            <strong>0.03%</strong>
            <small>6 events · last 24 hours</small>
          </article>
          <article>
            <span>Sync queue</span>
            <strong>0</strong>
            <small>Last run 18 seconds ago</small>
          </article>
        </section>
        <section className="admin-grid">
          <article className="admin-panel chart-panel">
            <header>
              <div>
                <span>Processed messages</span>
                <strong>2,842</strong>
              </div>
              <select aria-label="Chart range">
                <option>Last 7 days</option>
              </select>
            </header>
            <svg viewBox="0 0 700 180" role="img" aria-label="Processed messages over seven days">
              <path className="grid-line" d="M0 35H700 M0 90H700 M0 145H700" />
              <path
                className="chart-area"
                d="M0 145 C80 132 95 110 170 117 S280 42 350 72 S450 105 520 62 S630 24 700 42 L700 180 L0 180Z"
              />
              <path
                className="chart-line"
                d="M0 145 C80 132 95 110 170 117 S280 42 350 72 S450 105 520 62 S630 24 700 42"
              />
            </svg>
          </article>
          <article className="admin-panel">
            <header>
              <strong>Services</strong>
              <Button size="sm">View details</Button>
            </header>
            <div className="service-table">
              {serviceRows.map(([name, status, detail]) => (
                <div key={name}>
                  <span className="online-dot" />
                  <strong>{name}</strong>
                  <span>{status}</span>
                  <code>{detail}</code>
                </div>
              ))}
            </div>
          </article>
        </section>
        <section className="admin-panel recent-events">
          <header>
            <strong>Recent events</strong>
            <Button size="sm">Open logs</Button>
          </header>
          <div className="event-row">
            <time>18:42:03</time>
            <span className="event-level info">INFO</span>
            <code>sync.completed</code>
            <span>Google account synchronized</span>
            <small>req_01JQ…</small>
          </div>
          <div className="event-row">
            <time>18:41:58</time>
            <span className="event-level warn">WARN</span>
            <code>provider.retry</code>
            <span>Microsoft throttled a delta request</span>
            <small>req_01JQ…</small>
          </div>
          <div className="event-row">
            <time>18:40:12</time>
            <span className="event-level info">INFO</span>
            <code>backup.verified</code>
            <span>Local repository snapshot verified</span>
            <small>job_01JQ…</small>
          </div>
        </section>
      </main>
    </div>
  );
}

export function AccountsPage({ locale }: { locale: Locale }) {
  const navigate = useNavigate();
  const t: Translator = (key) => translate(locale, key);
  const [configured, setConfigured] = useState(false);
  const [accounts, setAccounts] = useState<MailAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  const reload = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError(false);
    try {
      const result = await loadGoogleAccounts(signal);
      setConfigured(result.status.configured);
      setAccounts(result.accounts);
    } catch {
      if (!signal?.aborted) setError(true);
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void reload(controller.signal);
    return () => controller.abort();
  }, [reload]);

  const connect = async (reconsent: boolean) => {
    setError(false);
    try {
      await startGoogleConnection(reconsent);
    } catch {
      setError(true);
    }
  };

  const disconnect = async (accountId: string) => {
    setError(false);
    try {
      await disconnectAccount(accountId);
      await reload();
    } catch {
      setError(true);
    }
  };

  return (
    <div className="settings-shell">
      <header className="admin-header">
        <Brand />
        <Button variant="outline" onClick={() => navigate("/")}>
          <ArrowLeft size={16} /> {t("viewInbox")}
        </Button>
      </header>
      <main className="settings-content" aria-labelledby="accounts-title">
        <div className="settings-heading">
          <div>
            <span>{t("settings")}</span>
            <h1 id="accounts-title">{t("connectedAccounts")}</h1>
            <p>{t("connectedAccountsDescription")}</p>
          </div>
          <Button
            variant="primary"
            disabled={!configured || loading}
            onClick={() => void connect(false)}
          >
            <Plus size={16} /> {t("connectGoogle")}
          </Button>
        </div>
        {new URLSearchParams(window.location.search).get("google") === "connected" && (
          <p className="settings-notice" role="status">
            {t("googleConnected")}
          </p>
        )}
        {!configured && !loading && (
          <div className="settings-notice warning">
            <Info size={17} />
            <span>{t("googleNotConfigured")}</span>
            <a href="https://github.com/Tutitoos/mailflow/blob/main/docs/providers/google.md">
              {t("googleSetupGuide")}
            </a>
          </div>
        )}
        {error && (
          <p className="auth-error" role="alert">
            {t("connectionFailed")}
          </p>
        )}
        <section className="account-list" aria-busy={loading}>
          {loading && <p>{t("loadingAccounts")}</p>}
          {!loading && configured && accounts.length === 0 && <p>{t("noGoogleAccounts")}</p>}
          {accounts.map((account) => (
            <article key={account.id}>
              <span className="provider-icon">G</span>
              <div>
                <strong>{account.displayName}</strong>
                <small>{account.syncState}</small>
              </div>
              <Button variant="outline" disabled={!configured} onClick={() => void connect(true)}>
                {t("reconnectGoogle")}
              </Button>
              <Button variant="outline" onClick={() => void disconnect(account.id)}>
                <Trash2 size={15} /> {t("disconnect")}
              </Button>
            </article>
          ))}
        </section>
      </main>
    </div>
  );
}
