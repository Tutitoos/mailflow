import { isDesktopRuntime } from "./desktop-runtime";
import type { InboxThread } from "./mailflow-api";

export type NativeNotificationPrivacy = "hidden" | "sender" | "full";
export type NativePermission = "not_determined" | "denied" | "authorized" | "unsupported";

export type NativeExperienceState = {
  notificationsEnabled: boolean;
  privacy: NativeNotificationPrivacy;
  permission: NativePermission;
};

type NativeCommand = "compose" | "search" | "inbox" | "refresh" | "settings";
type NotificationTarget = { accountId: string; threadId: string };

async function invokeNative<T>(command: string, args?: Record<string, unknown>) {
  const { invoke } = await import("@tauri-apps/api/core");
  return invoke<T>(command, args);
}

export async function loadNativeExperience() {
  if (!isDesktopRuntime()) return null;
  return invokeNative<NativeExperienceState>("native_experience_state");
}

export async function setNativeNotificationsEnabled(enabled: boolean) {
  return invokeNative<NativeExperienceState>("native_notifications_enable", { enabled });
}

export async function setNativeNotificationPrivacy(privacy: NativeNotificationPrivacy) {
  return invokeNative<NativeExperienceState>("native_notifications_set_privacy", { privacy });
}

export async function notifyNativeNewMail(accountId: string, thread: InboxThread) {
  if (!isDesktopRuntime() || thread.isRead) return;
  const eventId = `${accountId}:${thread.id}:${thread.lastMessageAt}`;
  await invokeNative("native_notify_new_mail", {
    input: {
      eventId,
      accountId,
      threadId: thread.id,
      sender: thread.senderName,
      subject: thread.subject,
    },
  });
}

export async function setNativeUnreadBadge(count: number) {
  if (!isDesktopRuntime()) return;
  await invokeNative("native_set_unread_badge", {
    count: Math.max(0, Math.min(999_999, Math.trunc(count))),
  });
}

export async function takePendingNativeNotification() {
  if (!isDesktopRuntime()) return null;
  return invokeNative<NotificationTarget | null>("native_notification_take_pending");
}

export function newUnreadThreads(known: ReadonlySet<string> | undefined, current: InboxThread[]) {
  if (!known) return [];
  return current
    .filter((thread) => !thread.isRead && !known.has(thread.id))
    .sort((left, right) => right.lastMessageAt.localeCompare(left.lastMessageAt))
    .slice(0, 3);
}

export async function listenForNativeExperience(
  onCommand: (command: NativeCommand) => void,
  onNotificationOpen: (target: NotificationTarget) => void,
) {
  if (!isDesktopRuntime()) return () => undefined;
  const { listen } = await import("@tauri-apps/api/event");
  const removeCommand = await listen<{ command?: unknown }>("mailflow:native-command", (event) => {
    const command = event.payload.command;
    if (
      command === "compose" ||
      command === "search" ||
      command === "inbox" ||
      command === "refresh" ||
      command === "settings"
    ) {
      onCommand(command);
    }
  });
  const removeNotification = await listen<Partial<NotificationTarget>>(
    "mailflow:notification-open",
    (event) => {
      const { accountId, threadId } = event.payload;
      if (validResourceId(accountId) && validResourceId(threadId)) {
        void takePendingNativeNotification()
          .then((pending) => onNotificationOpen(pending ?? { accountId, threadId }))
          .catch(() => onNotificationOpen({ accountId, threadId }));
      }
    },
  );
  return () => {
    removeCommand();
    removeNotification();
  };
}

function validResourceId(value: unknown): value is string {
  return (
    typeof value === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)
  );
}
