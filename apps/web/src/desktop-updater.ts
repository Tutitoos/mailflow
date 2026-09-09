import { isDesktopRuntime } from "./desktop-runtime";
import type { TranslationKey } from "./i18n";

export type DesktopUpdatePhase =
  | "idle"
  | "checking"
  | "up_to_date"
  | "available"
  | "downloading"
  | "cancelled"
  | "installing"
  | "restart_required"
  | "error";

export type DesktopUpdateCandidate = {
  version: string;
  notes: string;
  publishedAt: string | null;
  identity: string;
};

export type DesktopUpdateState = {
  configured: boolean;
  currentVersion: string;
  channel: "stable" | "beta" | null;
  phase: DesktopUpdatePhase;
  candidate: DesktopUpdateCandidate | null;
  downloadedBytes: number;
  totalBytes: number | null;
  errorCode: string | null;
};

async function invokeUpdater<T>(command: string, args?: Record<string, unknown>) {
  const { invoke } = await import("@tauri-apps/api/core");
  return invoke<T>(command, args);
}

export async function loadDesktopUpdater() {
  if (!isDesktopRuntime()) return null;
  return invokeUpdater<DesktopUpdateState>("native_updater_state");
}

export async function checkDesktopUpdate() {
  return invokeUpdater<DesktopUpdateState>("native_updater_check");
}

export async function installDesktopUpdate(identity: string) {
  return invokeUpdater<DesktopUpdateState>("native_updater_install", { identity });
}

export async function cancelDesktopUpdate() {
  return invokeUpdater<boolean>("native_updater_cancel");
}

export async function restartDesktopAfterUpdate() {
  return invokeUpdater<void>("native_updater_restart");
}

export async function listenForDesktopUpdater(onState: (state: DesktopUpdateState) => void) {
  if (!isDesktopRuntime()) return () => undefined;
  const { listen } = await import("@tauri-apps/api/event");
  return listen<DesktopUpdateState>("mailflow:update-state", (event) => onState(event.payload));
}

export function desktopUpdatePercent(state: DesktopUpdateState) {
  if (!state.totalBytes || state.totalBytes <= 0) return null;
  return Math.max(0, Math.min(100, Math.round((state.downloadedBytes / state.totalBytes) * 100)));
}

const updateErrors: Record<string, TranslationKey> = {
  update_candidate_missing: "admin.updater.error.candidateMissing",
  update_check_failed: "admin.updater.error.checkFailed",
  update_config_invalid: "admin.updater.error.configInvalid",
  update_download_failed: "admin.updater.error.downloadFailed",
  update_downgrade_rejected: "admin.updater.error.downgrade",
  update_identity_changed: "admin.updater.error.identity",
  update_incompatible: "admin.updater.error.incompatible",
  update_install_failed: "admin.updater.error.installFailed",
  update_manifest_invalid: "admin.updater.error.manifest",
  update_not_configured: "admin.updater.error.notConfigured",
  update_operation_in_progress: "admin.updater.error.inProgress",
  update_unsupported: "admin.updater.error.unsupported",
  update_wrong_channel: "admin.updater.error.channel",
};

export function desktopUpdateErrorKey(code: string | null): TranslationKey {
  return (code && updateErrors[code]) || "admin.updater.error.unknown";
}
