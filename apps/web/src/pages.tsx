import {
  Archive,
  ArrowLeft,
  Bell,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronsUpDown,
  CircleUserRound,
  Clock3,
  FileText,
  Forward,
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
  Reply,
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
import { useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { Button } from "./components/ui/button";
import { type Category, categories, type MailItem, messages } from "./data";
import { type Locale, type TranslationKey, translate } from "./i18n";

type Translator = (key: TranslationKey) => string;

function Brand() {
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
  onMenu,
  t,
}: {
  locale: Locale;
  onLocaleChange: () => void;
  query: string;
  onQueryChange: (value: string) => void;
  onMenu: () => void;
  t: Translator;
}) {
  const navigate = useNavigate();
  return (
    <header className="topbar">
      <div className="topbar-start">
        <Button size="icon" aria-label="Toggle navigation" onClick={onMenu}>
          <Menu size={19} />
        </Button>
        <Brand />
      </div>
      <label className="search-box">
        <Search size={18} aria-hidden="true" />
        <input
          value={query}
          onChange={(event) => onQueryChange(event.target.value)}
          placeholder={t("search")}
          aria-label={t("search")}
        />
        <kbd>/</kbd>
        <Button size="icon" aria-label="Search filters">
          <SlidersHorizontal size={17} />
        </Button>
      </label>
      <div className="topbar-actions">
        <span className="sync-dot" title={t("syncing")} />
        <Button size="icon" aria-label={t("admin")} onClick={() => navigate("/admin")}>
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
  t,
}: {
  collapsed: boolean;
  onCompose: () => void;
  t: Translator;
}) {
  return (
    <aside className={collapsed ? "sidebar collapsed" : "sidebar"}>
      <Button className="compose-button" variant="primary" onClick={onCompose}>
        <Pencil size={18} />
        <span>{t("compose")}</span>
      </Button>
      <nav aria-label="Mailboxes" className="nav-list">
        {mailboxItems.map(([key, Icon, count], index) => (
          <button className={index === 0 ? "nav-item active" : "nav-item"} type="button" key={key}>
            <Icon size={17} />
            <span>{t(key)}</span>
            {count && <strong>{count}</strong>}
          </button>
        ))}
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
        <button className="nav-item" type="button">
          <span className="label-dot violet" />
          <span>Development</span>
          <strong>46</strong>
        </button>
        <button className="nav-item" type="button">
          <span className="label-dot amber" />
          <span>Receipts</span>
          <strong>8</strong>
        </button>
        <button className="nav-item" type="button">
          <span className="label-dot blue" />
          <span>Travel</span>
        </button>
      </nav>
      <div className="sidebar-section-title account-title">
        <span>{t("accounts")}</span>
      </div>
      <nav className="nav-list accounts" aria-label={t("accounts")}>
        <button className="nav-item" type="button">
          <span className="provider-icon">G</span>
          <span>Personal</span>
          <span className="online-dot" />
        </button>
        <button className="nav-item" type="button">
          <span className="provider-icon">M</span>
          <span>Work</span>
          <span className="online-dot" />
        </button>
        <button className="nav-item" type="button">
          <span className="provider-icon">i</span>
          <span>iCloud</span>
          <span className="online-dot" />
        </button>
      </nav>
    </aside>
  );
}

function MailToolbar({
  allSelected,
  onSelectAll,
  range,
  t,
}: {
  allSelected: boolean;
  onSelectAll: () => void;
  range: string;
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
        <Button size="icon" aria-label={t("refresh")}>
          <RefreshCw size={17} />
        </Button>
        <Button size="icon" aria-label="More actions">
          <MoreHorizontal size={18} />
        </Button>
      </div>
      <div className="toolbar-group range-controls">
        <span>{range}</span>
        <Button size="icon" aria-label="Previous page">
          <ChevronLeft size={17} />
        </Button>
        <Button size="icon" aria-label="Next page">
          <ChevronRight size={17} />
        </Button>
      </div>
    </div>
  );
}

function CategoryTabs({
  active,
  onChange,
  t,
}: {
  active: Category;
  onChange: (category: Category) => void;
  t: Translator;
}) {
  const icons = { primary: Inbox, promotions: Tag, social: Users, updates: Info, forums: Mail };
  return (
    <div className="category-tabs" role="tablist" aria-label="Inbox categories">
      {categories.map((category) => {
        const Icon = icons[category.id];
        const count = messages.filter(
          (message) => message.category === category.id && message.unread,
        ).length;
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
  onOpen,
  onStar,
}: {
  message: MailItem;
  selected: boolean;
  starred: boolean;
  onSelect: () => void;
  onOpen: () => void;
  onStar: () => void;
}) {
  return (
    <article
      className={`message-row${message.unread ? " unread" : ""}${selected ? " selected" : ""}`}
    >
      <button
        className={selected ? "select-box checked" : "select-box"}
        type="button"
        onClick={onSelect}
        aria-label={`Select ${message.subject}`}
      >
        {selected ? "✓" : ""}
      </button>
      <button
        className={starred ? "row-icon starred" : "row-icon"}
        type="button"
        onClick={onStar}
        aria-label={`Star ${message.subject}`}
      >
        <Star size={16} fill={starred ? "currentColor" : "none"} />
      </button>
      <button
        className="row-icon important"
        type="button"
        aria-label={`Mark ${message.subject} important`}
      >
        <ChevronsUpDown size={16} />
      </button>
      <button className="message-content" type="button" onClick={onOpen}>
        <span className="sender">{message.sender}</span>
        <span className="subject-line">
          <strong>{message.subject}</strong>
          <span> — {message.preview}</span>
        </span>
        {message.attachment && (
          <span className="attachment-chip">
            <Paperclip size={13} />
            {message.attachment}
          </span>
        )}
        <span className="provider-badge" title={message.provider}>
          {message.provider.slice(0, 1)}
        </span>
        <time>{message.date}</time>
      </button>
      <div className="quick-actions">
        <Button size="icon" aria-label="Archive">
          <Archive size={16} />
        </Button>
        <Button size="icon" aria-label="Delete">
          <Trash2 size={16} />
        </Button>
        <Button size="icon" aria-label="Mark unread">
          <Mail size={16} />
        </Button>
        <Button size="icon" aria-label="Snooze">
          <Clock3 size={16} />
        </Button>
      </div>
    </article>
  );
}

function Conversation({
  message,
  onBack,
  t,
}: {
  message: MailItem;
  onBack: () => void;
  t: Translator;
}) {
  return (
    <section className="conversation">
      <div className="conversation-toolbar">
        <Button size="icon" aria-label={t("back")} onClick={onBack}>
          <ArrowLeft size={18} />
        </Button>
        <Button size="icon" aria-label={t("archive")}>
          <Archive size={17} />
        </Button>
        <Button size="icon" aria-label={t("delete")}>
          <Trash2 size={17} />
        </Button>
        <Button size="icon" aria-label={t("markUnread")}>
          <Mail size={17} />
        </Button>
        <Button size="icon" aria-label="Label">
          <Tag size={17} />
        </Button>
        <Button size="icon" aria-label="More">
          <MoreHorizontal size={18} />
        </Button>
      </div>
      <div className="conversation-heading">
        <div>
          <h1>{message.subject}</h1>
          <span className="thread-label">Inbox</span>
        </div>
        <span className="provider-pill">{message.provider}</span>
      </div>
      <article className="mail-body-card">
        <div className="sender-avatar">{message.sender.slice(0, 1)}</div>
        <div className="mail-body-main">
          <header>
            <div>
              <strong>{message.sender}</strong>
              <span>&lt;{message.senderEmail}&gt;</span>
            </div>
            <div>
              <time>{message.date}</time>
              <Button size="icon" aria-label={t("reply")}>
                <Reply size={16} />
              </Button>
              <Button size="icon" aria-label="More">
                <MoreHorizontal size={17} />
              </Button>
            </div>
          </header>
          <p>
            to me <ChevronDown size={13} />
          </p>
          <div className="message-body">
            <p>{message.body}</p>
            <p>
              Best,
              <br />
              {message.sender}
            </p>
          </div>
          {message.attachment && (
            <button className="attachment-card" type="button">
              <FileText size={24} />
              <span>
                <strong>{message.attachment}</strong>
                <small>128 KB · PDF</small>
              </span>
            </button>
          )}
          <div className="reply-actions">
            <Button variant="outline">
              <Reply size={16} />
              {t("reply")}
            </Button>
            <Button variant="outline">
              <Forward size={16} />
              {t("forward")}
            </Button>
          </div>
        </div>
      </article>
    </section>
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

export function MailPage() {
  const [locale, setLocale] = useState<Locale>("en");
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState<Category>("primary");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [starred, setStarred] = useState<Set<string>>(
    new Set(messages.filter((item) => item.starred).map((item) => item.id)),
  );
  const [activeMessage, setActiveMessage] = useState<MailItem | null>(null);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [composeOpen, setComposeOpen] = useState(false);
  const [composeMaximized, setComposeMaximized] = useState(false);
  const t: Translator = (key) => translate(locale, key);

  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return messages.filter((message) => {
      if (message.category !== category) return false;
      return (
        !normalized ||
        `${message.sender} ${message.subject} ${message.preview}`.toLowerCase().includes(normalized)
      );
    });
  }, [category, query]);

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
        onMenu={() => setSidebarCollapsed((value) => !value)}
        t={t}
      />
      <div className="app-body">
        <Sidebar collapsed={sidebarCollapsed} onCompose={() => setComposeOpen(true)} t={t} />
        <main className="mail-surface">
          {activeMessage ? (
            <Conversation message={activeMessage} onBack={() => setActiveMessage(null)} t={t} />
          ) : (
            <>
              <MailToolbar
                allSelected={
                  filtered.length > 0 && filtered.every((message) => selected.has(message.id))
                }
                onSelectAll={() =>
                  setSelected(
                    filtered.every((message) => selected.has(message.id))
                      ? new Set()
                      : new Set(filtered.map((message) => message.id)),
                  )
                }
                range={`1–${filtered.length} of ${messages.length}`}
                t={t}
              />
              <CategoryTabs active={category} onChange={setCategory} t={t} />
              <section className="message-list" aria-live="polite">
                {filtered.length ? (
                  filtered.map((message) => (
                    <MessageRow
                      key={message.id}
                      message={message}
                      selected={selected.has(message.id)}
                      starred={starred.has(message.id)}
                      onSelect={() => toggleSelection(message.id)}
                      onOpen={() => setActiveMessage(message)}
                      onStar={() =>
                        setStarred((current) => {
                          const next = new Set(current);
                          if (next.has(message.id)) next.delete(message.id);
                          else next.add(message.id);
                          return next;
                        })
                      }
                    />
                  ))
                ) : (
                  <div className="empty-state">
                    <Inbox size={30} />
                    <strong>{t("noMessages")}</strong>
                  </div>
                )}
              </section>
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
