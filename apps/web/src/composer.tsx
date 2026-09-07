import { $generateHtmlFromNodes, $generateNodesFromDOM } from "@lexical/html";
import { LexicalComposer } from "@lexical/react/LexicalComposer";
import { useLexicalComposerContext } from "@lexical/react/LexicalComposerContext";
import { ContentEditable } from "@lexical/react/LexicalContentEditable";
import { LexicalErrorBoundary } from "@lexical/react/LexicalErrorBoundary";
import { HistoryPlugin } from "@lexical/react/LexicalHistoryPlugin";
import { OnChangePlugin } from "@lexical/react/LexicalOnChangePlugin";
import { RichTextPlugin } from "@lexical/react/LexicalRichTextPlugin";
import {
  $createParagraphNode,
  $createTextNode,
  $getRoot,
  type EditorState,
  FORMAT_TEXT_COMMAND,
  type LexicalEditor,
} from "lexical";
import { Bold, Italic, Minus, Paperclip, Send, Square, Trash2, Underline, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button } from "./components/ui/button";
import { type Locale, translate } from "./i18n";
import {
  checkpointDraft,
  createDraft,
  type DraftAttachment,
  type DraftContent,
  discardDraft,
  loadDraft,
  type MailDraft,
  sendDraft,
  updateDraft,
  uploadDraftAttachment,
} from "./mailflow-api";

export type ComposeContext = {
  mode: "new" | "reply" | "forward";
  sourceMessageId?: string;
  to?: string;
  subject?: string;
  bodyText?: string;
};

type EditorContent = { text: string; html: string };

export const DRAFT_LOCAL_SAVE_MS = 2_000;
export const DRAFT_REMOTE_CHECKPOINT_MS = 15_000;

function InitialContentPlugin({ text }: { text: string }) {
  const [editor] = useLexicalComposerContext();
  const initialized = useRef(false);
  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;
    editor.update(() => {
      const root = $getRoot();
      root.clear();
      for (const line of text.split("\n")) {
        root.append($createParagraphNode().append($createTextNode(line)));
      }
    });
  }, [editor, text]);
  return null;
}

function EditorReferencePlugin({ onReady }: { onReady: (editor: LexicalEditor) => void }) {
  const [editor] = useLexicalComposerContext();
  useEffect(() => onReady(editor), [editor, onReady]);
  return null;
}

function FormatToolbar({ editor }: { editor: LexicalEditor | null }) {
  const format = (kind: "bold" | "italic" | "underline") =>
    editor?.dispatchCommand(FORMAT_TEXT_COMMAND, kind);
  return (
    <div className="compose-format" aria-label="Formatting" role="toolbar">
      <Button size="icon" aria-label="Bold" onClick={() => format("bold")}>
        <Bold size={15} />
      </Button>
      <Button size="icon" aria-label="Italic" onClick={() => format("italic")}>
        <Italic size={15} />
      </Button>
      <Button size="icon" aria-label="Underline" onClick={() => format("underline")}>
        <Underline size={15} />
      </Button>
    </div>
  );
}

function parseRecipients(value: string, role: "to" | "cc" | "bcc") {
  return value
    .split(/[;,]/u)
    .map((address) => address.trim())
    .filter(Boolean)
    .map((address) => ({ role, address }));
}

function recoveryKey(accountId: string) {
  return `mailflow:draft:${accountId}`;
}

export function ComposePanel({
  accountId,
  context,
  locale,
  maximized,
  onMaximize,
  onClose,
}: {
  accountId: string;
  context: ComposeContext;
  locale: Locale;
  maximized: boolean;
  onMaximize: () => void;
  onClose: () => void;
}) {
  const t = (key: Parameters<typeof translate>[1]) => translate(locale, key);
  const [minimized, setMinimized] = useState(false);
  const [showCopies, setShowCopies] = useState(false);
  const [to, setTo] = useState(context.to ?? "");
  const [cc, setCc] = useState("");
  const [bcc, setBcc] = useState("");
  const [subject, setSubject] = useState(context.subject ?? "");
  const [editorContent, setEditorContent] = useState<EditorContent>({
    text: context.bodyText ?? "",
    html: "",
  });
  const [draft, setDraft] = useState<MailDraft | null>(null);
  const [dirty, setDirty] = useState(true);
  const [status, setStatus] = useState<
    "editing" | "saving" | "saved" | "sending" | "conflict" | "ambiguous"
  >("editing");
  const [editor, setEditor] = useState<LexicalEditor | null>(null);
  const [attachments, setAttachments] = useState<DraftAttachment[]>([]);
  const [upload, setUpload] = useState<{ name: string; loaded: number; total: number } | null>(
    null,
  );
  const uploadController = useRef<AbortController | null>(null);
  const fileInput = useRef<HTMLInputElement | null>(null);
  const saveInFlight = useRef<Promise<MailDraft> | null>(null);
  const sendKey = useRef<string | null>(null);
  const recovered = useRef(false);
  const editVersion = useRef(0);
  const latest = useRef({ to, cc, bcc, subject, editorContent, attachments, draft, dirty });
  latest.current = { to, cc, bcc, subject, editorContent, attachments, draft, dirty };

  const markDirty = useCallback(() => {
    editVersion.current += 1;
    latest.current.dirty = true;
    setDirty(true);
    setStatus("editing");
  }, []);

  const content = useCallback(
    (revision?: number): DraftContent => ({
      accountId,
      ...(revision ? { expectedRevision: revision } : {}),
      subject: latest.current.subject,
      bodyText: latest.current.editorContent.text,
      bodyHtml: latest.current.editorContent.html,
      recipients: [
        ...parseRecipients(latest.current.to, "to"),
        ...parseRecipients(latest.current.cc, "cc"),
        ...parseRecipients(latest.current.bcc, "bcc"),
      ],
      attachments: latest.current.attachments,
      mode: context.mode,
      ...(context.sourceMessageId ? { sourceMessageId: context.sourceMessageId } : {}),
    }),
    [accountId, context.mode, context.sourceMessageId],
  );

  const saveLocal = useCallback(async (): Promise<MailDraft> => {
    if (saveInFlight.current) {
      const saved = await saveInFlight.current;
      return latest.current.dirty ? saveLocal() : saved;
    }
    const version = editVersion.current;
    const current = latest.current.draft;
    if (current && !latest.current.dirty) return current;
    const payload = content(current?.localRevision);
    const operation = (async () => {
      setStatus("saving");
      const saved = current ? await updateDraft(current.id, payload) : await createDraft(payload);
      latest.current.draft = saved;
      setDraft(saved);
      if (editVersion.current === version) {
        latest.current.dirty = false;
        setDirty(false);
        setStatus("saved");
      }
      localStorage.setItem(recoveryKey(accountId), JSON.stringify({ draftId: saved.id }));
      return saved;
    })().catch((error) => {
      setStatus("conflict");
      throw error;
    });
    saveInFlight.current = operation;
    try {
      const saved = await operation;
      if (latest.current.dirty) {
        saveInFlight.current = null;
        return saveLocal();
      }
      return saved;
    } finally {
      saveInFlight.current = null;
    }
  }, [accountId, content]);

  const checkpoint = useCallback(async () => {
    const saved =
      latest.current.dirty || !latest.current.draft ? await saveLocal() : latest.current.draft;
    if (!saved) throw new Error("draft_unavailable");
    const remote = await checkpointDraft(accountId, saved.id);
    setDraft(remote);
    setStatus("saved");
    return remote;
  }, [accountId, saveLocal]);

  useEffect(() => {
    if (!editor || recovered.current || context.mode !== "new") return;
    recovered.current = true;
    const raw = localStorage.getItem(recoveryKey(accountId));
    if (!raw) return;
    let draftId = "";
    try {
      const stored = JSON.parse(raw) as { draftId?: unknown };
      if (typeof stored.draftId === "string") draftId = stored.draftId;
    } catch {
      localStorage.removeItem(recoveryKey(accountId));
    }
    if (!draftId) return;
    void loadDraft(accountId, draftId)
      .then((saved) => {
        if (saved.syncStatus === "discarded") return;
        latest.current.draft = saved;
        latest.current.dirty = false;
        setDraft(saved);
        setTo(
          saved.recipients
            .filter((item) => item.role === "to")
            .map((item) => item.address)
            .join(", "),
        );
        setCc(
          saved.recipients
            .filter((item) => item.role === "cc")
            .map((item) => item.address)
            .join(", "),
        );
        setBcc(
          saved.recipients
            .filter((item) => item.role === "bcc")
            .map((item) => item.address)
            .join(", "),
        );
        setShowCopies(saved.recipients.some((item) => item.role !== "to"));
        setSubject(saved.subject);
        setAttachments(saved.attachments);
        setEditorContent({ text: saved.bodyText, html: saved.bodyHtml });
        editor.update(() => {
          const root = $getRoot();
          root.clear();
          if (saved.bodyHtml) {
            const document = new DOMParser().parseFromString(saved.bodyHtml, "text/html");
            root.append(...$generateNodesFromDOM(editor, document));
          } else {
            for (const line of saved.bodyText.split("\n")) {
              root.append($createParagraphNode().append($createTextNode(line)));
            }
          }
        });
        setDirty(false);
        setStatus(saved.syncStatus === "conflict" ? "conflict" : "saved");
      })
      .catch(() => localStorage.removeItem(recoveryKey(accountId)));
  }, [accountId, context.mode, editor]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: content fields intentionally restart the two-second debounce.
  useEffect(() => {
    const timer = window.setTimeout(
      () => void saveLocal().catch(() => undefined),
      DRAFT_LOCAL_SAVE_MS,
    );
    return () => window.clearTimeout(timer);
  }, [to, cc, bcc, subject, editorContent, attachments, saveLocal]);

  useEffect(() => {
    const interval = window.setInterval(
      () => void checkpoint().catch(() => undefined),
      DRAFT_REMOTE_CHECKPOINT_MS,
    );
    return () => window.clearInterval(interval);
  }, [checkpoint]);

  useEffect(() => {
    const protect = (event: BeforeUnloadEvent) => {
      if (!latest.current.dirty) return;
      event.preventDefault();
    };
    window.addEventListener("beforeunload", protect);
    return () => window.removeEventListener("beforeunload", protect);
  }, []);

  const onChange = useCallback(
    (state: EditorState, lexicalEditor: LexicalEditor) => {
      state.read(() => {
        setEditorContent({
          text: $getRoot().getTextContent(),
          html: $generateHtmlFromNodes(lexicalEditor),
        });
        markDirty();
      });
    },
    [markDirty],
  );

  const requestClose = async () => {
    if (uploadController.current) {
      setStatus("conflict");
      return;
    }
    try {
      await checkpoint();
      onClose();
    } catch {
      setStatus("conflict");
    }
  };

  const discard = async () => {
    try {
      uploadController.current?.abort();
      if (latest.current.draft) await discardDraft(accountId, latest.current.draft.id);
      localStorage.removeItem(recoveryKey(accountId));
      onClose();
    } catch {
      setStatus("conflict");
    }
  };

  const send = async () => {
    if (uploadController.current) return;
    try {
      setStatus("sending");
      const saved = await saveLocal();
      sendKey.current ??= `mailflow-send-${crypto.randomUUID()}`;
      const delivery = await sendDraft(accountId, saved.id, saved.localRevision, sendKey.current);
      if (delivery.status === "sent") {
        localStorage.removeItem(recoveryKey(accountId));
        onClose();
      } else if (delivery.status === "ambiguous") {
        setStatus("ambiguous");
      } else {
        setStatus("sending");
      }
    } catch {
      setStatus("conflict");
    }
  };

  const uploadFile = async (file: File) => {
    if (uploadController.current) return;
    const controller = new AbortController();
    uploadController.current = controller;
    setUpload({ name: file.name, loaded: 0, total: file.size });
    try {
      const stored = await uploadDraftAttachment(
        accountId,
        file,
        (loaded, total) => setUpload({ name: file.name, loaded, total }),
        controller.signal,
      );
      setAttachments((current) => [...current, stored]);
      markDirty();
    } catch {
      if (!controller.signal.aborted) setStatus("conflict");
    } finally {
      if (uploadController.current === controller) uploadController.current = null;
      setUpload(null);
      if (fileInput.current) fileInput.current.value = "";
    }
  };

  const panelClass = ["compose-panel", maximized ? "maximized" : "", minimized ? "minimized" : ""]
    .filter(Boolean)
    .join(" ");
  const statusText = useMemo(
    () => translate(locale, `draft.${status}` as Parameters<typeof translate>[1]),
    [locale, status],
  );
  return (
    <section
      className={panelClass}
      data-sentry-block
      aria-label={t("newMessage")}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget))
          void checkpoint().catch(() => undefined);
      }}
    >
      <header>
        <strong>
          {context.mode === "reply"
            ? t("reply")
            : context.mode === "forward"
              ? t("forward")
              : t("newMessage")}
        </strong>
        <span className={`draft-status ${status}`} role="status">
          {statusText}
        </span>
        <div>
          <Button
            size="icon"
            aria-label={t("minimize")}
            onClick={() => setMinimized((value) => !value)}
          >
            <Minus size={15} />
          </Button>
          <Button size="icon" aria-label={t("maximize")} onClick={onMaximize}>
            <Square size={13} />
          </Button>
          <Button size="icon" aria-label={t("close")} onClick={() => void requestClose()}>
            <X size={16} />
          </Button>
        </div>
      </header>
      {!minimized && (
        <>
          <label className="compose-field">
            <span>{t("to")}</span>
            <input
              aria-label={t("recipients")}
              value={to}
              onChange={(event) => {
                setTo(event.target.value);
                markDirty();
              }}
            />
            <button
              type="button"
              onClick={() => setShowCopies((value) => !value)}
              aria-expanded={showCopies}
            >
              Cc Bcc
            </button>
          </label>
          {showCopies && (
            <div className="compose-copies">
              <label className="compose-field">
                <span>Cc</span>
                <input
                  aria-label="Cc"
                  value={cc}
                  onChange={(event) => {
                    setCc(event.target.value);
                    markDirty();
                  }}
                />
              </label>
              <label className="compose-field">
                <span>Bcc</span>
                <input
                  aria-label="Bcc"
                  value={bcc}
                  onChange={(event) => {
                    setBcc(event.target.value);
                    markDirty();
                  }}
                />
              </label>
            </div>
          )}
          <label className="compose-field">
            <input
              aria-label={t("subject")}
              placeholder={t("subject")}
              value={subject}
              onChange={(event) => {
                setSubject(event.target.value);
                markDirty();
              }}
            />
          </label>
          <LexicalComposer
            initialConfig={{
              namespace: "MailflowComposer",
              onError: () => setStatus("conflict"),
              theme: {},
            }}
          >
            <InitialContentPlugin text={context.bodyText ?? ""} />
            <EditorReferencePlugin onReady={setEditor} />
            <div className="compose-editor-shell">
              <RichTextPlugin
                contentEditable={
                  <ContentEditable className="compose-editor" aria-label={t("message")} />
                }
                placeholder={<span className="compose-placeholder">{t("message")}</span>}
                ErrorBoundary={LexicalErrorBoundary}
              />
            </div>
            <HistoryPlugin />
            <OnChangePlugin onChange={onChange} />
          </LexicalComposer>
          <footer>
            <div>
              <Button
                variant="primary"
                disabled={status === "sending" || status === "ambiguous"}
                onClick={() => void send()}
              >
                <Send size={16} /> {t("send")}
              </Button>
              <FormatToolbar editor={editor} />
              <input
                ref={fileInput}
                className="compose-file-input"
                type="file"
                aria-label={t("addAttachment")}
                onChange={(event) => {
                  const file = event.target.files?.[0];
                  if (file) void uploadFile(file);
                }}
              />
              <Button
                size="icon"
                aria-label={t("addAttachment")}
                disabled={upload !== null}
                onClick={() => fileInput.current?.click()}
              >
                <Paperclip size={16} />
              </Button>
            </div>
            <Button size="icon" aria-label={t("discardDraft")} onClick={() => void discard()}>
              <Trash2 size={17} />
            </Button>
          </footer>
          {(attachments.length > 0 || upload) && (
            <ul className="compose-attachments" aria-label={t("attachments")}>
              {attachments.map((attachment) => (
                <li key={attachment.objectId}>
                  <span>{attachment.filename || t("attachment")}</span>
                  <small>{attachment.mediaType}</small>
                  <Button
                    size="icon"
                    aria-label={t("removeAttachment")}
                    onClick={() => {
                      setAttachments((current) =>
                        current.filter((item) => item.objectId !== attachment.objectId),
                      );
                      markDirty();
                    }}
                  >
                    <X size={14} />
                  </Button>
                </li>
              ))}
              {upload && (
                <li>
                  <span>{upload.name}</span>
                  <progress
                    value={upload.loaded}
                    max={Math.max(upload.total, 1)}
                    aria-label={t("uploadProgress")}
                  />
                  <Button
                    size="icon"
                    aria-label={t("cancelUpload")}
                    onClick={() => uploadController.current?.abort()}
                  >
                    <X size={14} />
                  </Button>
                </li>
              )}
            </ul>
          )}
        </>
      )}
    </section>
  );
}
