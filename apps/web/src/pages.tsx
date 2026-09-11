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
  Folder,
  Inbox,
  Info,
  KeyRound,
  Languages,
  LogOut,
  Mail,
  MailOpen,
  Menu,
  MoreHorizontal,
  PanelRightClose,
  Paperclip,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Send,
  Settings,
  SlidersHorizontal,
  Star,
  Tag,
  Trash2,
  Users,
} from "lucide-react";
import { type CSSProperties, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { registerPasskey, signOut } from "./auth-client";
import { Button } from "./components/ui/button";
import { type ComposeContext, ComposePanel } from "./composer";
import { ConversationView } from "./conversation";
import { desktopConnectivityEvent, isDesktopCacheFallback } from "./desktop-cache";
import {
  listenForNativeExperience,
  loadNativeExperience,
  type NativeExperienceState,
  type NativeNotificationPrivacy,
  newUnreadThreads,
  notifyNativeNewMail,
  setNativeNotificationPrivacy,
  setNativeNotificationsEnabled,
  setNativeUnreadBadge,
  takePendingNativeNotification,
} from "./desktop-native";
import { isDesktopRuntime } from "./desktop-runtime";
import { installTranslationCatalog, type Locale, type TranslationKey, translate } from "./i18n";
import {
  APIError,
  accountSupports,
  connectICloudAccount,
  connectIMAPAccount,
  createMailActions,
  disconnectAccount,
  discoverIMAPFolders,
  type IMAPAccountInput,
  type InboxThread,
  loadAccountConnections,
  loadInboxPage,
  loadMailAccounts,
  loadMailNavigation,
  loadTranslationCatalog,
  type MailAccount,
  type MailActionKind,
  type Mailbox,
  type MailCategory,
  type MailLabel,
  probeICloudAccount,
  probeIMAPAccount,
  refreshAccountCredentials,
  type SearchResult,
  searchMail,
  startGoogleConnection,
  startMicrosoftConnection,
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
  accounts,
  activeAccountId,
  onAccountChange,
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
  accounts: MailAccount[];
  activeAccountId: string | null;
  onAccountChange: (accountId: string) => void;
  t: Translator;
}) {
  const navigate = useNavigate();
  const searchInput = useRef<HTMLInputElement>(null);
  const [showSearchHelp, setShowSearchHelp] = useState(false);
  const [showAccounts, setShowAccounts] = useState(false);
  const accountMenu = useRef<HTMLDivElement>(null);
  const activeAccount = accounts.find(({ id }) => id === activeAccountId);
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
    const focusNativeSearch = () => searchInput.current?.focus();
    window.addEventListener("keydown", focusSearch);
    window.addEventListener("mailflow:focus-search", focusNativeSearch);
    return () => {
      window.removeEventListener("keydown", focusSearch);
      window.removeEventListener("mailflow:focus-search", focusNativeSearch);
    };
  }, []);
  useEffect(() => {
    if (!showAccounts) return;
    const close = (event: PointerEvent) => {
      if (!accountMenu.current?.contains(event.target as Node)) setShowAccounts(false);
    };
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setShowAccounts(false);
    };
    window.addEventListener("pointerdown", close);
    window.addEventListener("keydown", handleEscape);
    return () => {
      window.removeEventListener("pointerdown", close);
      window.removeEventListener("keydown", handleEscape);
    };
  }, [showAccounts]);
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
            autoComplete="off"
            aria-expanded={showSearchHelp}
            aria-controls="mailflow-search-suggestions"
            role="combobox"
            onFocus={() => setShowSearchHelp(true)}
            onBlur={() => window.setTimeout(() => setShowSearchHelp(false), 120)}
          />
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
            <fieldset className="search-help" id="mailflow-search-suggestions">
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
        <div className="account-menu-anchor" ref={accountMenu}>
          <button
            className="avatar"
            type="button"
            aria-label="Account menu"
            aria-haspopup="menu"
            aria-expanded={showAccounts}
            onClick={() => setShowAccounts((value) => !value)}
          >
            {(activeAccount?.displayName || "M").slice(0, 1).toUpperCase()}
          </button>
          {showAccounts && (
            <div className="account-popover ui-popover" role="menu" aria-label={t("accounts")}>
              <header>
                <span>{t("accounts")}</span>
                <small>{accounts.length}</small>
              </header>
              {accounts.map((account) => (
                <button
                  type="button"
                  role="menuitemradio"
                  aria-checked={account.id === activeAccountId}
                  className="account-option ui-menu-item"
                  key={account.id}
                  onClick={() => {
                    onAccountChange(account.id);
                    setShowAccounts(false);
                  }}
                >
                  <span className="provider-icon">
                    {account.provider.slice(0, 1).toUpperCase()}
                  </span>
                  <span>
                    <strong>{account.displayName}</strong>
                    <small>{account.provider}</small>
                  </span>
                  <span className={account.syncState === "error" ? "offline-dot" : "online-dot"} />
                </button>
              ))}
              <button
                className="ui-menu-item"
                type="button"
                onClick={() => navigate("/settings/accounts")}
              >
                <Plus size={16} /> {t("connectedAccounts")}
              </button>
            </div>
          )}
        </div>
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

type LabelTreeNode = {
  name: string;
  path: string;
  label?: MailLabel;
  children: LabelTreeNode[];
};

export function buildLabelTree(labels: MailLabel[]): LabelTreeNode[] {
  const roots: LabelTreeNode[] = [];
  for (const label of labels) {
    const parts = (label.localName || label.remoteName).split("/").filter(Boolean);
    let siblings = roots;
    let path = "";
    parts.forEach((name, index) => {
      path = path ? `${path}/${name}` : name;
      let node = siblings.find((candidate) => candidate.name === name);
      if (!node) {
        node = { name, path, children: [] };
        siblings.push(node);
      }
      if (index === parts.length - 1) node.label = label;
      siblings = node.children;
    });
  }
  const sort = (nodes: LabelTreeNode[]): LabelTreeNode[] =>
    nodes
      .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }))
      .map((node) => ({ ...node, children: sort(node.children) }));
  return sort(roots);
}

function LabelBranch({
  node,
  depth,
  collapsed,
  onToggle,
  onSelect,
}: {
  node: LabelTreeNode;
  depth: number;
  collapsed: Set<string>;
  onToggle: (path: string) => void;
  onSelect: (label: MailLabel) => void;
}) {
  const hasChildren = node.children.length > 0;
  const isCollapsed = collapsed.has(node.path);
  return (
    <div className="label-branch">
      <div className="label-tree-row" style={{ "--label-depth": depth } as CSSProperties}>
        {hasChildren ? (
          <button
            className="label-disclosure"
            type="button"
            aria-label={`${isCollapsed ? "Expand" : "Collapse"} ${node.name}`}
            aria-expanded={!isCollapsed}
            onClick={() => onToggle(node.path)}
          >
            <ChevronRight size={14} />
          </button>
        ) : (
          <span className="label-disclosure-spacer" />
        )}
        <button
          className="nav-item label-item"
          type="button"
          disabled={!node.label}
          onClick={() => node.label && onSelect(node.label)}
        >
          <Folder size={15} fill="currentColor" />
          <span>{node.name}</span>
          {(node.label?.unreadCount || 0) > 0 && <strong>{node.label?.unreadCount}</strong>}
        </button>
      </div>
      {hasChildren && !isCollapsed && (
        <div>
          {node.children.map((child) => (
            <LabelBranch
              key={child.path}
              node={child}
              depth={depth + 1}
              collapsed={collapsed}
              onToggle={onToggle}
              onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function Sidebar({
  collapsed,
  onCompose,
  mailboxes,
  labels,
  onLabelSelect,
  t,
  canCompose,
}: {
  collapsed: boolean;
  onCompose: () => void;
  mailboxes: Mailbox[];
  labels: MailLabel[];
  onLabelSelect: (label: MailLabel) => void;
  t: Translator;
  canCompose: boolean;
}) {
  const mailboxByRole = new Map(mailboxes.map((mailbox) => [mailbox.role, mailbox]));
  const [collapsedLabels, setCollapsedLabels] = useState<Set<string>>(new Set());
  const userLabels = useMemo(
    () => buildLabelTree(labels.filter((label) => label.kind === "user")),
    [labels],
  );
  return (
    <aside className={collapsed ? "sidebar collapsed" : "sidebar"}>
      <Button
        className="compose-button"
        variant="primary"
        aria-label={t("compose")}
        onClick={onCompose}
        disabled={!canCompose}
      >
        <Pencil size={18} />
        <span>{t("compose")}</span>
      </Button>
      <nav aria-label="Mailboxes" className="nav-list">
        {mailboxItems.map(([key, Icon], index) => {
          const role = key === "allMail" ? "all" : key;
          const mailbox = mailboxByRole.get(role as Mailbox["role"]);
          const count = mailbox?.unreadCount ?? 0;
          const label = mailbox?.localName || mailbox?.remoteName || t(key);
          return (
            <button
              aria-label={label}
              className={index === 0 ? "nav-item active" : "nav-item"}
              type="button"
              key={key}
            >
              <Icon size={17} />
              <span>{label}</span>
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
        {userLabels.map((node) => (
          <LabelBranch
            key={node.path}
            node={node}
            depth={0}
            collapsed={collapsedLabels}
            onToggle={(path) =>
              setCollapsedLabels((current) => {
                const next = new Set(current);
                if (next.has(path)) next.delete(path);
                else next.add(path);
                return next;
              })
            }
            onSelect={onLabelSelect}
          />
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
  onReadState,
  readAction,
  onLabel,
  t,
  actionsEnabled,
  labelsEnabled,
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
  onReadState: () => void;
  readAction: "mark_read" | "mark_unread";
  onLabel: () => void;
  t: Translator;
  actionsEnabled: boolean;
  labelsEnabled: boolean;
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
        {selectedCount > 0 && actionsEnabled && (
          <>
            <Button size="icon" aria-label={t("archive")} onClick={onArchive}>
              <Archive size={17} />
            </Button>
            <Button size="icon" aria-label={t("delete")} onClick={onTrash}>
              <Trash2 size={17} />
            </Button>
            <Button
              size="icon"
              aria-label={t(readAction === "mark_read" ? "markRead" : "markUnread")}
              onClick={onReadState}
            >
              <Mail size={17} />
            </Button>
            {labelsEnabled && (
              <Button size="icon" aria-label={t("applyLabel")} onClick={onLabel}>
                <Tag size={17} />
              </Button>
            )}
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
  onReadToggle,
  onImportant,
  readActionLabel,
  openLabel,
  actionsEnabled,
}: {
  message: InboxThread;
  selected: boolean;
  starred: boolean;
  onSelect: () => void;
  onStar: () => void;
  onOpen: () => void;
  onArchive: () => void;
  onTrash: () => void;
  onReadToggle: () => void;
  onImportant: () => void;
  readActionLabel: string;
  openLabel: string;
  actionsEnabled: boolean;
}) {
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    const handleEscape = (event: KeyboardEvent) => event.key === "Escape" && close();
    window.addEventListener("pointerdown", close);
    window.addEventListener("keydown", handleEscape);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("pointerdown", close);
      window.removeEventListener("keydown", handleEscape);
      window.removeEventListener("blur", close);
    };
  }, [menu]);
  return (
    <article
      className={`message-row${!message.isRead ? " unread" : ""}${selected ? " selected" : ""}`}
      onContextMenu={(event) => {
        event.preventDefault();
        setMenu({
          x: Math.max(8, Math.min(event.clientX, window.innerWidth - 224)),
          y: Math.max(8, Math.min(event.clientY, window.innerHeight - 290)),
        });
      }}
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
        aria-label={`${starred ? "Unstar" : "Star"} ${message.subject || message.senderName}`}
        disabled={!actionsEnabled}
      >
        <Star size={16} fill={starred ? "currentColor" : "none"} />
      </button>
      <button
        className="row-icon important"
        type="button"
        aria-label={`Mark ${message.subject || message.senderName} important`}
        onClick={onImportant}
        disabled={!actionsEnabled}
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
      {actionsEnabled && (
        <div className="quick-actions">
          <Button size="icon" aria-label="Archive" onClick={onArchive}>
            <Archive size={16} />
          </Button>
          <Button size="icon" aria-label="Delete" onClick={onTrash}>
            <Trash2 size={16} />
          </Button>
          <Button size="icon" aria-label={readActionLabel} onClick={onReadToggle}>
            <Mail size={16} />
          </Button>
        </div>
      )}
      {menu && (
        <div
          className="message-context-menu ui-menu"
          role="menu"
          aria-label={`Actions for ${message.subject || message.senderName}`}
          style={{ left: menu.x, top: menu.y }}
          onPointerDown={(event) => event.stopPropagation()}
        >
          <button
            className="ui-menu-item"
            role="menuitem"
            type="button"
            onClick={() => {
              onOpen();
              setMenu(null);
            }}
          >
            <MailOpen size={16} /> {openLabel}
          </button>
          {actionsEnabled && (
            <>
              <button
                className="ui-menu-item"
                role="menuitem"
                type="button"
                onClick={() => {
                  onArchive();
                  setMenu(null);
                }}
              >
                <Archive size={16} /> Archive
              </button>
              <button
                className="ui-menu-item"
                role="menuitem"
                type="button"
                onClick={() => {
                  onReadToggle();
                  setMenu(null);
                }}
              >
                <Mail size={16} /> {readActionLabel}
              </button>
              <button
                className="ui-menu-item"
                role="menuitem"
                type="button"
                onClick={() => {
                  onImportant();
                  setMenu(null);
                }}
              >
                <Tag size={16} /> {message.isImportant ? "Mark unimportant" : "Mark important"}
              </button>
              <button
                className="ui-menu-item danger"
                role="menuitem"
                type="button"
                onClick={() => {
                  onTrash();
                  setMenu(null);
                }}
              >
                <Trash2 size={16} /> Delete
              </button>
            </>
          )}
        </div>
      )}
    </article>
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
  onSelect,
  onStar,
  onOpen,
  onAction,
  t,
  actionsEnabled,
}: {
  messages: InboxThread[];
  selected: Set<string>;
  onSelect: (id: string) => void;
  onStar: (id: string, starred: boolean) => void;
  onOpen: (id: string) => void;
  onAction: (kind: MailActionKind, ids: string[]) => void;
  t: Translator;
  actionsEnabled: boolean;
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
                starred={message.isStarred}
                onSelect={() => onSelect(message.id)}
                onOpen={() => onOpen(message.id)}
                onStar={() => onStar(message.id, message.isStarred)}
                onArchive={() => onAction("archive", [message.id])}
                onTrash={() => onAction("move_to_trash", [message.id])}
                onReadToggle={() =>
                  onAction(message.isRead ? "mark_unread" : "mark_read", [message.id])
                }
                onImportant={() =>
                  onAction(message.isImportant ? "mark_unimportant" : "mark_important", [
                    message.id,
                  ])
                }
                readActionLabel={`${t(message.isRead ? "markUnread" : "markRead")} ${
                  message.subject || message.senderName
                }`}
                openLabel={`${t("openMessage")} ${message.subject || message.senderName}`}
                actionsEnabled={actionsEnabled}
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
  const [, setTranslationRevision] = useState(0);
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
  const [online, setOnline] = useState(() => navigator.onLine && !isDesktopCacheFallback());
  const [eventsConnected, setEventsConnected] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [actionNotice, setActionNotice] = useState<"pending" | "failed" | "partial" | null>(null);
  const actionKeys = useRef(new Map<string, string>());
  const knownNativeThreads = useRef(new Map<string, Set<string>>());
  const nativeUnreadByAccount = useRef(new Map<string, number>());
  const [sidebarCollapsed, setSidebarCollapsed] = useState(
    () => window.matchMedia("(max-width: 900px)").matches,
  );
  const [composeOpen, setComposeOpen] = useState(false);
  const [composeMaximized, setComposeMaximized] = useState(false);
  const [composeContext, setComposeContext] = useState<ComposeContext>({ mode: "new" });
  const t: Translator = (key) => translate(locale, key);
  const activeAccount = accounts.find((account) => account.id === activeAccountId);
  const canActions = activeAccount ? accountSupports(activeAccount, "actions") : false;
  const canCompose = activeAccount
    ? accountSupports(activeAccount, "drafts") && accountSupports(activeAccount, "send")
    : false;
  const canAttachments = activeAccount ? accountSupports(activeAccount, "attachments") : false;
  const canCategories = activeAccount ? accountSupports(activeAccount, "categories") : false;
  const canLabels = activeAccount ? accountSupports(activeAccount, "labels") : false;
  const actionLabelId = canLabels ? labels.find((label) => label.kind === "user")?.id : undefined;

  const refreshNativeAccount = useCallback(
    async (accountId: string, shouldNotify: boolean, signal?: AbortSignal) => {
      if (!isDesktopRuntime()) return;
      const [page, navigation] = await Promise.all([
        loadInboxPage(accountId, "primary", undefined, signal),
        loadMailNavigation(accountId, signal),
      ]);
      const known = knownNativeThreads.current.get(accountId);
      const unseen = shouldNotify ? newUnreadThreads(known, page.items) : [];
      knownNativeThreads.current.set(accountId, new Set(page.items.map(({ id }) => id)));
      const inbox = navigation.mailboxes.find(({ role }) => role === "inbox");
      nativeUnreadByAccount.current.set(accountId, inbox?.unreadCount ?? 0);
      const totalUnread = [...nativeUnreadByAccount.current.values()].reduce(
        (total, count) => total + count,
        0,
      );
      await setNativeUnreadBadge(totalUnread).catch(() => undefined);
      await Promise.allSettled(unseen.map((thread) => notifyNativeNewMail(accountId, thread)));
    },
    [],
  );

  useEffect(() => {
    const controller = new AbortController();
    void loadTranslationCatalog(locale, controller.signal)
      .then((catalog) => {
        if (installTranslationCatalog(catalog)) setTranslationRevision(catalog.revision);
      })
      .catch(() => undefined);
    return () => controller.abort();
  }, [locale]);

  useEffect(() => {
    if (!isDesktopRuntime() || accounts.length === 0) return;
    const controller = new AbortController();
    for (const account of accounts) {
      void refreshNativeAccount(account.id, false, controller.signal).catch(() => undefined);
    }
    return () => controller.abort();
  }, [accounts, refreshNativeAccount]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: native commands must use the active account and current capabilities.
  useEffect(() => {
    if (!isDesktopRuntime()) return;
    let disposed = false;
    let removeListeners: () => void = () => undefined;
    const openTarget = ({ accountId, threadId }: { accountId: string; threadId: string }) => {
      if (!accounts.some(({ id }) => id === accountId)) return;
      setSubmittedSearch("");
      setQuery("");
      setSearchResults([]);
      setActiveAccountId(accountId);
      setActiveThreadId(threadId);
    };
    const command = (value: "compose" | "search" | "inbox" | "refresh" | "settings") => {
      if (value === "compose" && canCompose) {
        setComposeContext({ mode: "new" });
        setComposeOpen(true);
      } else if (value === "search") {
        window.dispatchEvent(new Event("mailflow:focus-search"));
      } else if (value === "inbox") {
        setSubmittedSearch("");
        setQuery("");
        setSearchResults([]);
        setCategory("primary");
        setActiveThreadId(null);
        window.history.replaceState(null, "", "/");
      } else if (value === "refresh") {
        setRefreshRevision((current) => current + 1);
      } else if (value === "settings") {
        void navigate("/settings/accounts");
      }
    };
    void takePendingNativeNotification()
      .then((target) => {
        if (!disposed && target) openTarget(target);
      })
      .catch(() => undefined);
    void listenForNativeExperience(command, openTarget)
      .then((remove) => {
        if (disposed) remove();
        else removeListeners = remove;
      })
      .catch(() => undefined);
    return () => {
      disposed = true;
      removeListeners();
    };
  }, [accounts, activeAccountId, canCompose]);

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
    if (!activeAccountId) return;
    const controller = new AbortController();
    setLoadState("loading");
    void loadInboxPage(activeAccountId, category, pageCursor, controller.signal)
      .then((page) => {
        setThreads(page.items);
        setNextCursor(page.nextCursor);
        setLoadState("ready");
      })
      .catch(() => {
        if (!controller.signal.aborted) setLoadState("error");
      });
    return () => controller.abort();
  }, [activeAccountId, category, pageCursor, refreshRevision]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: refreshRevision intentionally reloads the current search cursor.
  useEffect(() => {
    if (!activeAccountId || !submittedSearch) return;
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
  }, [activeAccountId, refreshRevision, searchCursor, submittedSearch]);

  useEffect(() => {
    const becameOnline = () => {
      setOnline(true);
      setRefreshRevision((current) => current + 1);
    };
    const becameOffline = () => setOnline(false);
    const desktopConnectivity = (event: Event) => {
      const offline = (event as CustomEvent<{ offline?: boolean }>).detail?.offline === true;
      setOnline(navigator.onLine && !offline);
    };
    window.addEventListener("online", becameOnline);
    window.addEventListener("offline", becameOffline);
    window.addEventListener(desktopConnectivityEvent, desktopConnectivity);
    return () => {
      window.removeEventListener("online", becameOnline);
      window.removeEventListener("offline", becameOffline);
      window.removeEventListener(desktopConnectivityEvent, desktopConnectivity);
    };
  }, []);

  useEffect(() => {
    if (!online) return;
    const controller = new AbortController();
    void subscribeMailEvents(
      (event) => {
        const eventAccount = event.payload.accountId;
        const affectsActiveAccount =
          typeof eventAccount !== "string" || !activeAccountId || eventAccount === activeAccountId;
        if (event.type === "system.resync_required" && affectsActiveAccount) setLoadState("resync");
        if (event.type === "translations.changed") {
          void loadTranslationCatalog(locale, controller.signal)
            .then((catalog) => {
              if (installTranslationCatalog(catalog)) setTranslationRevision(catalog.revision);
            })
            .catch(() => undefined);
        }
        if (event.type === "mail.changed" || event.type === "sync.progress") {
          if (affectsActiveAccount) setRefreshRevision((current) => current + 1);
          if (typeof eventAccount === "string") {
            void refreshNativeAccount(eventAccount, true, controller.signal).catch(() => undefined);
          }
        }
      },
      setEventsConnected,
      controller.signal,
    ).catch(() => setEventsConnected(false));
    return () => controller.abort();
  }, [activeAccountId, locale, online, refreshNativeAccount]);

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
  const selectedReadAction = displayedThreads.some(
    (thread) => selected.has(thread.id) && !thread.isRead,
  )
    ? "mark_read"
    : "mark_unread";

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
    if (!activeAccountId || !canActions || targetIds.length === 0) return;
    if (!online) {
      setActionNotice("failed");
      return;
    }
    const signature = `${kind}:${[...targetIds].sort().join(",")}:${labelId ?? ""}`;
    const key = actionKeys.current.get(signature) ?? `mailflow-${crypto.randomUUID()}`;
    actionKeys.current.set(signature, key);
    const previousThreads = threads;
    const previousSearch = searchResults;
    const targets = new Set(targetIds);
    const updateThread = (item: InboxThread) => ({
      ...item,
      isRead: kind === "mark_read" ? true : kind === "mark_unread" ? false : item.isRead,
      isStarred: kind === "star" ? true : kind === "unstar" ? false : item.isStarred,
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
                isStarred: kind === "star" ? true : kind === "unstar" ? false : item.isStarred,
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
    setSelected((current) => new Set([...current].filter((id) => !targets.has(id))));
    setActionNotice("pending");
    try {
      const result = await createMailActions(activeAccountId, kind, targetIds, key, labelId);
      setActionNotice(result.partial ? "partial" : null);
      if (result.partial) setRefreshRevision((current) => current + 1);
    } catch {
      setThreads(previousThreads);
      setSearchResults(previousSearch);
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
        selected.size === 0 ||
        !canActions
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
  }, [selected, activeAccountId, online, threads, searchResults]);

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
        accounts={accounts}
        activeAccountId={activeAccountId}
        onAccountChange={(accountId) => {
          setPageCursor(undefined);
          setCursorHistory([]);
          setActiveThreadId(null);
          setActiveAccountId(accountId);
        }}
        t={t}
      />
      <div className={`app-body${sidebarCollapsed ? " sidebar-is-collapsed" : ""}`}>
        <Sidebar
          collapsed={sidebarCollapsed}
          onCompose={() => {
            setComposeContext({ mode: "new" });
            setComposeOpen(true);
            setSidebarCollapsed(true);
          }}
          mailboxes={mailboxes}
          labels={labels}
          onLabelSelect={(label) => {
            const nextQuery = `label:"${label.remoteName.replaceAll('"', '\\"')}"`;
            setQuery(nextQuery);
            submitSearch(nextQuery);
          }}
          t={t}
          canCompose={canCompose}
        />
        <main className="mail-surface">
          {activeThreadId && activeAccountId ? (
            <ConversationView
              accountId={activeAccountId}
              threadId={activeThreadId}
              locale={locale}
              onBack={() => setActiveThreadId(null)}
              onCompose={(context) => {
                setComposeContext(context);
                setComposeOpen(true);
                setSidebarCollapsed(true);
              }}
              onAction={(kind, labelId) => {
                void runAction(kind, [activeThreadId], labelId);
                if (kind === "archive" || kind === "move_to_trash") setActiveThreadId(null);
              }}
              canActions={canActions}
              canCompose={canCompose}
              canAttachments={canAttachments}
              labelId={actionLabelId}
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
                onReadState={() => void runAction(selectedReadAction, [...selected])}
                readAction={selectedReadAction}
                onLabel={() => {
                  if (actionLabelId) void runAction("add_label", [...selected], actionLabelId);
                }}
                t={t}
                actionsEnabled={canActions}
                labelsEnabled={Boolean(actionLabelId)}
              />
              {!searching && canCategories && (
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
              {!online && (
                <div className="partial-banner" role="status">
                  <strong>{t("offline")}</strong> ·{" "}
                  {t(isDesktopRuntime() ? "desktopOfflineDescription" : "offlineDescription")}
                </div>
              )}
              {activeLoadState === "loading" ? (
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
                  onSelect={toggleSelection}
                  onOpen={setActiveThreadId}
                  onAction={(kind, ids) => void runAction(kind, ids)}
                  t={t}
                  onStar={(id, isStarred) => void runAction(isStarred ? "unstar" : "star", [id])}
                  actionsEnabled={canActions}
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
      {composeOpen && activeAccountId && canCompose && (
        <ComposePanel
          accountId={activeAccountId}
          context={composeContext}
          locale={locale}
          maximized={composeMaximized}
          onMaximize={() => setComposeMaximized((value) => !value)}
          onClose={() => {
            setComposeOpen(false);
            setComposeMaximized(false);
          }}
        />
      )}
    </div>
  );
}

export function AccountsPage({ locale }: { locale: Locale }) {
  const navigate = useNavigate();
  const t: Translator = (key) => translate(locale, key);
  const [configured, setConfigured] = useState({ google: false, microsoft: false });
  const [accounts, setAccounts] = useState<MailAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<
    | "generic"
    | "reconsent"
    | "tenant"
    | "tls"
    | "timeout"
    | "authentication"
    | "icloudAuthentication"
    | "capability"
    | null
  >(null);
  const [mailSetup, setMailSetup] = useState<"imap" | "icloud" | null>(null);
  const [discoveringIMAP, setDiscoveringIMAP] = useState<string | null>(null);
  const [discoveredIMAP, setDiscoveredIMAP] = useState<Record<string, number>>({});
  const [imapStatus, setIMAPStatus] = useState<"idle" | "testing" | "verified" | "connecting">(
    "idle",
  );
  const [imapInput, setIMAPInput] = useState<IMAPAccountInput>({
    displayName: "",
    username: "",
    password: "",
    imap: { host: "", port: 993, tlsMode: "implicit" },
    smtp: { host: "", port: 587, tlsMode: "starttls" },
  });
  const [passkeyStatus, setPasskeyStatus] = useState<
    "idle" | "registering" | "registered" | "error"
  >("idle");
  const [nativeExperience, setNativeExperience] = useState<NativeExperienceState | null>(null);
  const [nativeExperienceError, setNativeExperienceError] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void loadNativeExperience()
      .then((state) => {
        if (!controller.signal.aborted) setNativeExperience(state);
      })
      .catch(() => {
        if (!controller.signal.aborted) setNativeExperienceError(true);
      });
    return () => controller.abort();
  }, []);

  const updateNativeNotifications = async (enabled: boolean) => {
    setNativeExperienceError(false);
    try {
      setNativeExperience(await setNativeNotificationsEnabled(enabled));
    } catch {
      setNativeExperienceError(true);
    }
  };

  const updateNativePrivacy = async (privacy: NativeNotificationPrivacy) => {
    setNativeExperienceError(false);
    try {
      setNativeExperience(await setNativeNotificationPrivacy(privacy));
    } catch {
      setNativeExperienceError(true);
    }
  };

  const logout = async () => {
    setError(null);
    try {
      await signOut();
      window.location.replace("/");
    } catch {
      setError("generic");
    }
  };

  const addPasskey = async () => {
    setPasskeyStatus("registering");
    try {
      await registerPasskey();
      setPasskeyStatus("registered");
    } catch {
      setPasskeyStatus("error");
    }
  };

  const reload = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError(null);
    try {
      const result = await loadAccountConnections(signal);
      setConfigured({
        google: result.status.google.configured,
        microsoft: result.status.microsoft.configured,
      });
      setAccounts(result.accounts);
    } catch {
      if (!signal?.aborted) setError("generic");
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void reload(controller.signal);
    return () => controller.abort();
  }, [reload]);

  const connect = async (provider: "google" | "microsoft", reconsent: boolean) => {
    setError(null);
    try {
      if (provider === "google") await startGoogleConnection(reconsent);
      else await startMicrosoftConnection(reconsent);
    } catch (cause) {
      setError(accountError(cause));
    }
  };

  const disconnect = async (accountId: string) => {
    setError(null);
    try {
      await disconnectAccount(accountId);
      await reload();
    } catch (cause) {
      setError(accountError(cause));
    }
  };

  const refresh = async (accountId: string) => {
    setError(null);
    let refreshError: ReturnType<typeof accountError> | null = null;
    try {
      await refreshAccountCredentials(accountId);
    } catch (cause) {
      refreshError = accountError(cause);
    } finally {
      await reload();
      if (refreshError) setError(refreshError);
    }
  };

  const discoverFolders = async (accountId: string) => {
    setError(null);
    setDiscoveringIMAP(accountId);
    try {
      const result = await discoverIMAPFolders(accountId);
      setDiscoveredIMAP((current) => ({ ...current, [accountId]: result.folders.length }));
    } catch (cause) {
      setError(accountError(cause));
    } finally {
      setDiscoveringIMAP(null);
    }
  };

  const resetIMAP = () => {
    setMailSetup(null);
    setIMAPStatus("idle");
    setIMAPInput({
      displayName: "",
      username: "",
      password: "",
      imap: { host: "", port: 993, tlsMode: "implicit" },
      smtp: { host: "", port: 587, tlsMode: "starttls" },
    });
  };

  const openMailSetup = (mode: "imap" | "icloud") => {
    setMailSetup(mode);
    setError(null);
    setIMAPStatus("idle");
    setIMAPInput({
      displayName: mode === "icloud" ? "iCloud" : "",
      username: "",
      password: "",
      imap: { host: mode === "icloud" ? "imap.mail.me.com" : "", port: 993, tlsMode: "implicit" },
      smtp: { host: mode === "icloud" ? "smtp.mail.me.com" : "", port: 587, tlsMode: "starttls" },
    });
  };

  const updateIMAPInput = (input: IMAPAccountInput) => {
    setIMAPInput(input);
    setIMAPStatus("idle");
  };

  const submitIMAP = async (mode: "probe" | "connect") => {
    setError(null);
    setIMAPStatus(mode === "probe" ? "testing" : "connecting");
    try {
      const iCloudInput = {
        displayName: imapInput.displayName,
        email: imapInput.username,
        appSpecificPassword: imapInput.password,
      };
      if (mode === "probe") {
        if (mailSetup === "icloud") await probeICloudAccount(iCloudInput);
        else await probeIMAPAccount(imapInput);
        setIMAPStatus("verified");
      } else {
        if (mailSetup === "icloud") await connectICloudAccount(iCloudInput);
        else await connectIMAPAccount(imapInput);
        resetIMAP();
        await reload();
      }
    } catch (cause) {
      setIMAPStatus("idle");
      setError(accountError(cause));
    }
  };

  return (
    <div className="settings-shell">
      <header className="admin-header">
        <Brand />
        <div className="settings-actions">
          <Button variant="outline" onClick={() => navigate("/")}>
            <ArrowLeft size={16} /> {t("viewInbox")}
          </Button>
          <Button variant="outline" onClick={() => void logout()}>
            <LogOut size={16} /> {t("signOut")}
          </Button>
        </div>
      </header>
      <main className="settings-content" aria-labelledby="accounts-title">
        <div className="settings-heading">
          <div>
            <span>{t("settings")}</span>
            <h1 id="accounts-title">{t("connectedAccounts")}</h1>
            <p>{t("connectedAccountsDescription")}</p>
          </div>
          <div className="settings-actions">
            <Button
              variant="primary"
              disabled={!configured.google || loading}
              onClick={() => void connect("google", false)}
            >
              <Plus size={16} /> {t("connectGoogle")}
            </Button>
            <Button
              variant="outline"
              disabled={!configured.microsoft || loading}
              onClick={() => void connect("microsoft", false)}
            >
              <Plus size={16} /> {t("connectMicrosoft")}
            </Button>
            <Button variant="outline" disabled={loading} onClick={() => openMailSetup("icloud")}>
              <Plus size={16} /> {t("connectICloud")}
            </Button>
            <Button variant="outline" disabled={loading} onClick={() => openMailSetup("imap")}>
              <Plus size={16} /> {t("connectIMAP")}
            </Button>
          </div>
        </div>
        <section className="settings-notice warning" aria-labelledby="passkey-settings-title">
          <KeyRound size={17} />
          <span>
            <strong id="passkey-settings-title">{t("passkeySettingsTitle")}</strong>
            <br />
            {t("passkeySettingsDescription")}
          </span>
          <Button
            variant="outline"
            disabled={passkeyStatus === "registering"}
            onClick={() => void addPasskey()}
          >
            {t("registerPasskey")}
          </Button>
          {passkeyStatus === "registered" && <span role="status">{t("passkeyRegistered")}</span>}
          {passkeyStatus === "error" && <span role="alert">{t("passkeyRegistrationFailed")}</span>}
        </section>
        {nativeExperience && (
          <section
            className="native-notification-settings"
            aria-labelledby="native-notification-title"
          >
            <div>
              <Bell size={18} />
              <span>
                <strong id="native-notification-title">{t("desktopNotificationsTitle")}</strong>
                <br />
                {t("desktopNotificationsDescription")}
              </span>
            </div>
            <label>
              <input
                type="checkbox"
                checked={nativeExperience.notificationsEnabled}
                onChange={(event) => void updateNativeNotifications(event.target.checked)}
              />
              {t("desktopNotificationsEnabled")}
            </label>
            <label>
              {t("desktopNotificationPrivacy")}
              <select
                value={nativeExperience.privacy}
                onChange={(event) =>
                  void updateNativePrivacy(event.target.value as NativeNotificationPrivacy)
                }
              >
                <option value="hidden">{t("desktopNotificationHidden")}</option>
                <option value="sender">{t("desktopNotificationSender")}</option>
                <option value="full">{t("desktopNotificationFull")}</option>
              </select>
            </label>
            {nativeExperience.permission === "denied" && (
              <p role="alert">{t("desktopNotificationDenied")}</p>
            )}
          </section>
        )}
        {nativeExperienceError && (
          <p className="settings-notice" data-tone="warning" role="alert">
            {t("desktopNotificationFailed")}
          </p>
        )}
        {mailSetup && (
          <form
            className="imap-form"
            onSubmit={(event) => {
              event.preventDefault();
              void submitIMAP("connect");
            }}
          >
            <div className="imap-form-heading">
              <div>
                <h2>{mailSetup === "icloud" ? t("icloudSetupTitle") : t("imapSetupTitle")}</h2>
                <p>
                  {mailSetup === "icloud" ? t("icloudSetupDescription") : t("imapSetupDescription")}
                </p>
              </div>
              <Button type="button" variant="outline" onClick={resetIMAP}>
                {t("cancel")}
              </Button>
            </div>
            <label>
              {t("accountName")}
              <input
                required
                maxLength={120}
                value={imapInput.displayName}
                onChange={(event) =>
                  updateIMAPInput({ ...imapInput, displayName: event.target.value })
                }
              />
            </label>
            <label>
              {mailSetup === "icloud" ? t("icloudEmail") : t("username")}
              <input
                required
                maxLength={320}
                type={mailSetup === "icloud" ? "email" : "text"}
                autoComplete={mailSetup === "icloud" ? "email" : "username"}
                value={imapInput.username}
                onChange={(event) =>
                  updateIMAPInput({ ...imapInput, username: event.target.value })
                }
              />
            </label>
            <label>
              {mailSetup === "icloud" ? t("appSpecificPassword") : t("password")}
              <input
                required
                maxLength={4096}
                type="password"
                autoComplete={mailSetup === "icloud" ? "off" : "current-password"}
                value={imapInput.password}
                onChange={(event) =>
                  updateIMAPInput({ ...imapInput, password: event.target.value })
                }
              />
            </label>
            {mailSetup === "icloud" && (
              <div className="icloud-preset-note">
                <strong>{t("icloudPresetTitle")}</strong>
                <span>{t("icloudPresetServers")}</span>
                <a href="https://account.apple.com" target="_blank" rel="noreferrer">
                  {t("createAppSpecificPassword")}
                </a>
              </div>
            )}
            {mailSetup === "imap" &&
              (["imap", "smtp"] as const).map((protocol) => (
                <fieldset key={protocol}>
                  <legend>{protocol.toUpperCase()}</legend>
                  <label>
                    {t("serverHost")}
                    <input
                      required
                      maxLength={253}
                      value={imapInput[protocol].host}
                      onChange={(event) =>
                        updateIMAPInput({
                          ...imapInput,
                          [protocol]: { ...imapInput[protocol], host: event.target.value },
                        })
                      }
                    />
                  </label>
                  <label>
                    {t("serverPort")}
                    <input
                      required
                      min={1}
                      max={65535}
                      type="number"
                      value={imapInput[protocol].port}
                      onChange={(event) =>
                        updateIMAPInput({
                          ...imapInput,
                          [protocol]: { ...imapInput[protocol], port: Number(event.target.value) },
                        })
                      }
                    />
                  </label>
                  <label>
                    {t("tlsMode")}
                    <select
                      value={imapInput[protocol].tlsMode}
                      onChange={(event) =>
                        updateIMAPInput({
                          ...imapInput,
                          [protocol]: {
                            ...imapInput[protocol],
                            tlsMode: event.target.value as "implicit" | "starttls",
                          },
                        })
                      }
                    >
                      <option value="implicit">{t("tlsImplicit")}</option>
                      <option value="starttls">STARTTLS</option>
                    </select>
                  </label>
                </fieldset>
              ))}
            <p className="imap-security-note">
              <Info size={16} />{" "}
              {mailSetup === "icloud" ? t("icloudSecurityNote") : t("imapSecurityNote")}
            </p>
            {imapStatus === "verified" && (
              <p className="settings-notice" role="status">
                {t("imapConnectionVerified")}
              </p>
            )}
            <div className="settings-actions">
              <Button
                type="button"
                variant="outline"
                disabled={imapStatus === "testing" || imapStatus === "connecting"}
                onClick={() => void submitIMAP("probe")}
              >
                {t("testConnection")}
              </Button>
              <Button
                type="submit"
                variant="primary"
                disabled={imapStatus === "testing" || imapStatus === "connecting"}
              >
                {mailSetup === "icloud" ? t("connectICloud") : t("connectIMAP")}
              </Button>
            </div>
          </form>
        )}
        {new URLSearchParams(window.location.search).get("google") === "connected" && (
          <p className="settings-notice" role="status">
            {t("googleConnected")}
          </p>
        )}
        {new URLSearchParams(window.location.search).get("microsoft") === "connected" && (
          <p className="settings-notice" role="status">
            {t("microsoftConnected")}
          </p>
        )}
        {(new URLSearchParams(window.location.search).get("google") === "failed" ||
          new URLSearchParams(window.location.search).get("microsoft") === "failed") && (
          <p className="settings-notice" data-tone="warning" role="status">
            {t("connectionFailed")}
          </p>
        )}
        {!configured.google && !loading && (
          <div className="settings-notice warning">
            <Info size={17} />
            <span>{t("googleNotConfigured")}</span>
            <a href="https://github.com/Tutitoos/mailflow/blob/main/docs/providers/google.md">
              {t("googleSetupGuide")}
            </a>
          </div>
        )}
        {!configured.microsoft && !loading && (
          <div className="settings-notice warning">
            <Info size={17} />
            <span>{t("microsoftNotConfigured")}</span>
            <a href="https://github.com/Tutitoos/mailflow/blob/main/docs/providers/microsoft.md">
              {t("microsoftSetupGuide")}
            </a>
          </div>
        )}
        {error && (
          <p className="auth-error" role="alert">
            {error === "reconsent"
              ? t("microsoftReconsentRequired")
              : error === "tenant"
                ? t("microsoftTenantPolicy")
                : error === "tls"
                  ? t("imapTLSFailed")
                  : error === "timeout"
                    ? t("imapTimeout")
                    : error === "icloudAuthentication"
                      ? t("icloudAuthenticationFailed")
                      : error === "authentication"
                        ? t("imapAuthenticationFailed")
                        : error === "capability"
                          ? t("imapCapabilityFailed")
                          : t("connectionFailed")}
          </p>
        )}
        <section className="account-list" aria-busy={loading}>
          {loading && <p>{t("loadingAccounts")}</p>}
          {!loading && accounts.length === 0 && <p>{t("noConnectedProviderAccounts")}</p>}
          {accounts.map((account) => (
            <article key={account.id}>
              <span className="provider-icon">
                {account.provider === "google" ? "G" : account.provider === "microsoft" ? "M" : "I"}
              </span>
              <div>
                <strong>{account.displayName}</strong>
                <small>{account.syncState}</small>
              </div>
              {account.provider !== "imap" && (
                <Button
                  variant="outline"
                  disabled={
                    account.provider === "google" ? !configured.google : !configured.microsoft
                  }
                  onClick={() =>
                    void connect(account.provider === "google" ? "google" : "microsoft", true)
                  }
                >
                  {account.provider === "google" ? t("reconnectGoogle") : t("reconnectMicrosoft")}
                </Button>
              )}
              {account.provider !== "imap" && (
                <Button
                  variant="outline"
                  disabled={account.syncState === "disabled"}
                  onClick={() => void refresh(account.id)}
                >
                  <RefreshCw size={15} /> {t("refreshAccess")}
                </Button>
              )}
              {account.provider === "imap" && (
                <Button
                  variant="outline"
                  disabled={account.syncState === "disabled" || discoveringIMAP === account.id}
                  onClick={() => void discoverFolders(account.id)}
                >
                  <RefreshCw size={15} />{" "}
                  {discoveringIMAP === account.id ? t("discoveringFolders") : t("discoverFolders")}
                </Button>
              )}
              {discoveredIMAP[account.id] !== undefined && (
                <small role="status">
                  {t("foldersDiscovered").replace("{count}", String(discoveredIMAP[account.id]))}
                </small>
              )}
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

function accountError(
  cause: unknown,
):
  | "generic"
  | "reconsent"
  | "tenant"
  | "tls"
  | "timeout"
  | "authentication"
  | "icloudAuthentication"
  | "capability" {
  if (cause instanceof APIError && cause.code === "microsoft_reconsent_required")
    return "reconsent";
  if (cause instanceof APIError && cause.code === "microsoft_tenant_policy") return "tenant";
  if (cause instanceof APIError && cause.code === "mail_tls_identity_failed") return "tls";
  if (cause instanceof APIError && cause.code === "mail_server_timeout") return "timeout";
  if (cause instanceof APIError && cause.code === "mail_authentication_failed")
    return "authentication";
  if (cause instanceof APIError && cause.code === "icloud_app_password_rejected")
    return "icloudAuthentication";
  if (cause instanceof APIError && cause.code === "mail_capability_failed") return "capability";
  return "generic";
}
