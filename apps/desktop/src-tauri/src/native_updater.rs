use semver::Version;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    sync::{Arc, Mutex},
    time::Duration,
};
use tauri::{AppHandle, Emitter, State, Url};
use tauri_plugin_updater::{Update, UpdaterExt};
use tokio::sync::watch;

const UPDATE_TARGET: &str = "darwin-universal";
const BUNDLE_IDENTIFIER: &str = "dev.tutitoos.mailflow";
const MAX_PUBLIC_KEY_BYTES: usize = 8 * 1024;
const MAX_NOTES_CHARS: usize = 4_000;
const UPDATE_TIMEOUT: Duration = Duration::from_secs(30);

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
enum UpdateChannel {
    Stable,
    Beta,
}

impl UpdateChannel {
    fn parse(value: Option<&str>) -> Result<Self, &'static str> {
        match value.unwrap_or("stable") {
            "stable" => Ok(Self::Stable),
            "beta" => Ok(Self::Beta),
            _ => Err("update_config_invalid"),
        }
    }

    fn as_str(self) -> &'static str {
        match self {
            Self::Stable => "stable",
            Self::Beta => "beta",
        }
    }

    fn accepts(self, version: &Version) -> bool {
        match self {
            Self::Stable => version.pre.is_empty(),
            Self::Beta => version.pre.as_str().split('.').next() == Some("beta"),
        }
    }
}

#[derive(Clone)]
struct UpdaterConfig {
    channel: UpdateChannel,
    endpoint: Url,
    public_key: String,
}

impl UpdaterConfig {
    fn compile_time() -> Result<Option<Self>, &'static str> {
        Self::parse(
            option_env!("MAILFLOW_DESKTOP_UPDATE_CHANNEL"),
            option_env!("MAILFLOW_DESKTOP_UPDATE_ENDPOINT"),
            option_env!("MAILFLOW_DESKTOP_UPDATE_PUBLIC_KEY"),
        )
    }

    fn parse(
        channel: Option<&str>,
        endpoint: Option<&str>,
        public_key: Option<&str>,
    ) -> Result<Option<Self>, &'static str> {
        if channel.is_none() && endpoint.is_none() && public_key.is_none() {
            return Ok(None);
        }
        let channel = UpdateChannel::parse(channel)?;
        let endpoint = endpoint.ok_or("update_config_invalid")?;
        let public_key = public_key.ok_or("update_config_invalid")?.trim();
        if public_key.len() < 32
            || public_key.len() > MAX_PUBLIC_KEY_BYTES
            || public_key
                .chars()
                .any(|character| character.is_control() && !matches!(character, '\n' | '\r' | '\t'))
        {
            return Err("update_config_invalid");
        }
        let endpoint = Url::parse(endpoint).map_err(|_| "update_config_invalid")?;
        if endpoint.scheme() != "https"
            || endpoint.host_str().is_none()
            || !endpoint.username().is_empty()
            || endpoint.password().is_some()
            || endpoint.fragment().is_some()
        {
            return Err("update_config_invalid");
        }
        Ok(Some(Self {
            channel,
            endpoint,
            public_key: public_key.to_string(),
        }))
    }
}

#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct MailflowManifest {
    channel: UpdateChannel,
    bundle_identifier: String,
    target: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateCandidate {
    version: String,
    notes: String,
    published_at: Option<String>,
    identity: String,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum UpdatePhase {
    #[default]
    Idle,
    Checking,
    UpToDate,
    Available,
    Downloading,
    Cancelled,
    Installing,
    RestartRequired,
    Error,
}

#[derive(Clone, Debug, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateSnapshot {
    configured: bool,
    current_version: String,
    channel: Option<UpdateChannel>,
    phase: UpdatePhase,
    candidate: Option<UpdateCandidate>,
    downloaded_bytes: u64,
    total_bytes: Option<u64>,
    error_code: Option<String>,
}

struct UpdateState {
    snapshot: UpdateSnapshot,
    candidate_identity: Option<String>,
}

pub struct NativeUpdater {
    config: Option<UpdaterConfig>,
    state: Mutex<UpdateState>,
    cancel: Mutex<Option<watch::Sender<bool>>>,
}

impl NativeUpdater {
    pub fn load(current_version: String) -> Self {
        let (config, error_code) = match UpdaterConfig::compile_time() {
            Ok(config) => (config, None),
            Err(code) => (None, Some(code.to_string())),
        };
        let configured = config.is_some() && cfg!(target_os = "macos");
        let channel = config.as_ref().map(|value| value.channel);
        Self {
            config,
            state: Mutex::new(UpdateState {
                snapshot: UpdateSnapshot {
                    configured,
                    current_version,
                    channel,
                    phase: if error_code.is_some() {
                        UpdatePhase::Error
                    } else {
                        UpdatePhase::Idle
                    },
                    error_code,
                    ..UpdateSnapshot::default()
                },
                candidate_identity: None,
            }),
            cancel: Mutex::new(None),
        }
    }

    fn snapshot(&self) -> Result<UpdateSnapshot, String> {
        self.state
            .lock()
            .map(|state| state.snapshot.clone())
            .map_err(|_| "update_state_unavailable".to_string())
    }

    fn set_phase(
        &self,
        app: &AppHandle,
        phase: UpdatePhase,
        error_code: Option<&str>,
    ) -> Result<UpdateSnapshot, String> {
        let snapshot = {
            let mut state = self
                .state
                .lock()
                .map_err(|_| "update_state_unavailable".to_string())?;
            state.snapshot.phase = phase;
            state.snapshot.error_code = error_code.map(str::to_string);
            state.snapshot.clone()
        };
        let _ = app.emit("mailflow:update-state", &snapshot);
        Ok(snapshot)
    }

    fn begin(&self, app: &AppHandle, phase: UpdatePhase) -> Result<UpdateSnapshot, String> {
        {
            let state = self
                .state
                .lock()
                .map_err(|_| "update_state_unavailable".to_string())?;
            if matches!(
                state.snapshot.phase,
                UpdatePhase::Checking | UpdatePhase::Downloading | UpdatePhase::Installing
            ) {
                return Err("update_operation_in_progress".to_string());
            }
        }
        self.set_phase(app, phase, None)
    }

    fn set_candidate(
        &self,
        app: &AppHandle,
        candidate: Option<UpdateCandidate>,
    ) -> Result<UpdateSnapshot, String> {
        let snapshot = {
            let mut state = self
                .state
                .lock()
                .map_err(|_| "update_state_unavailable".to_string())?;
            state.candidate_identity = candidate.as_ref().map(|value| value.identity.clone());
            state.snapshot.candidate = candidate;
            state.snapshot.downloaded_bytes = 0;
            state.snapshot.total_bytes = None;
            state.snapshot.phase = if state.snapshot.candidate.is_some() {
                UpdatePhase::Available
            } else {
                UpdatePhase::UpToDate
            };
            state.snapshot.error_code = None;
            state.snapshot.clone()
        };
        let _ = app.emit("mailflow:update-state", &snapshot);
        Ok(snapshot)
    }

    fn expected_identity(&self) -> Result<String, String> {
        self.state
            .lock()
            .map_err(|_| "update_state_unavailable".to_string())?
            .candidate_identity
            .clone()
            .ok_or_else(|| "update_candidate_missing".to_string())
    }

    fn progress(&self, app: &AppHandle, chunk: usize, total: Option<u64>) {
        let snapshot = self.state.lock().ok().map(|mut state| {
            state.snapshot.downloaded_bytes =
                state.snapshot.downloaded_bytes.saturating_add(chunk as u64);
            state.snapshot.total_bytes = total;
            state.snapshot.clone()
        });
        if let Some(snapshot) = snapshot {
            let _ = app.emit("mailflow:update-state", snapshot);
        }
    }

    fn install_cancel(&self, sender: watch::Sender<bool>) -> Result<(), String> {
        let mut cancel = self
            .cancel
            .lock()
            .map_err(|_| "update_state_unavailable".to_string())?;
        *cancel = Some(sender);
        Ok(())
    }

    fn clear_cancel(&self) {
        if let Ok(mut cancel) = self.cancel.lock() {
            *cancel = None;
        }
    }

    fn cancel_download(&self) -> Result<bool, String> {
        let cancel = self
            .cancel
            .lock()
            .map_err(|_| "update_state_unavailable".to_string())?;
        Ok(cancel
            .as_ref()
            .is_some_and(|sender| sender.send(true).is_ok()))
    }
}

fn updater_config(manager: &NativeUpdater) -> Result<UpdaterConfig, String> {
    if !cfg!(target_os = "macos") {
        return Err("update_unsupported".to_string());
    }
    manager
        .config
        .clone()
        .ok_or_else(|| "update_not_configured".to_string())
}

async fn fetch_update(app: &AppHandle, config: &UpdaterConfig) -> Result<Option<Update>, String> {
    app.updater_builder()
        .pubkey(&config.public_key)
        .target(UPDATE_TARGET)
        .endpoints(vec![config.endpoint.clone()])
        .map_err(|_| "update_config_invalid".to_string())?
        .timeout(UPDATE_TIMEOUT)
        .version_comparator(|_, _| true)
        .build()
        .map_err(|_| "update_config_invalid".to_string())?
        .check()
        .await
        .map_err(|_| "update_check_failed".to_string())
}

fn candidate_from_update(
    config: &UpdaterConfig,
    current_version: &str,
    update: &Update,
) -> Result<UpdateCandidate, String> {
    let metadata: MailflowManifest = serde_json::from_value(
        update
            .raw_json
            .get("mailflow")
            .cloned()
            .ok_or("update_manifest_invalid")?,
    )
    .map_err(|_| "update_manifest_invalid")?;
    let notes = update.body.as_deref().unwrap_or_default();
    validate_candidate_policy(
        config,
        current_version,
        &update.version,
        &update.target,
        &update.download_url,
        &metadata,
        notes,
    )?;
    let identity = release_identity(
        config.channel,
        &update.version,
        update.download_url.as_str(),
        &update.signature,
        &update.target,
    );
    Ok(UpdateCandidate {
        version: update.version.clone(),
        notes: notes.to_string(),
        published_at: update.date.map(|date| date.to_string()),
        identity,
    })
}

fn validate_candidate_policy(
    config: &UpdaterConfig,
    current_version: &str,
    update_version: &str,
    target: &str,
    download_url: &Url,
    metadata: &MailflowManifest,
    notes: &str,
) -> Result<(), String> {
    let current = Version::parse(current_version).map_err(|_| "update_config_invalid")?;
    let version = Version::parse(update_version).map_err(|_| "update_manifest_invalid")?;
    if version <= current {
        return Err("update_downgrade_rejected".to_string());
    }
    if !config.channel.accepts(&version) || metadata.channel != config.channel {
        return Err("update_wrong_channel".to_string());
    }
    if target != UPDATE_TARGET
        || download_url.scheme() != "https"
        || metadata.bundle_identifier != BUNDLE_IDENTIFIER
        || metadata.target != UPDATE_TARGET
    {
        return Err("update_incompatible".to_string());
    }
    if notes.chars().count() > MAX_NOTES_CHARS
        || notes
            .chars()
            .any(|character| character.is_control() && !matches!(character, '\n' | '\r' | '\t'))
    {
        return Err("update_manifest_invalid".to_string());
    }
    Ok(())
}

fn release_identity(
    channel: UpdateChannel,
    version: &str,
    url: &str,
    signature: &str,
    target: &str,
) -> String {
    let mut digest = Sha256::new();
    for value in [channel.as_str(), version, url, signature, target] {
        digest.update((value.len() as u64).to_be_bytes());
        digest.update(value.as_bytes());
    }
    hex::encode(digest.finalize())
}

fn fail(manager: &NativeUpdater, app: &AppHandle, code: &str) -> Result<UpdateSnapshot, String> {
    manager.clear_cancel();
    manager.set_phase(app, UpdatePhase::Error, Some(code))
}

#[tauri::command]
pub fn native_updater_state(
    manager: State<'_, Arc<NativeUpdater>>,
) -> Result<UpdateSnapshot, String> {
    manager.snapshot()
}

#[tauri::command]
pub async fn native_updater_check(
    app: AppHandle,
    manager: State<'_, Arc<NativeUpdater>>,
) -> Result<UpdateSnapshot, String> {
    manager.begin(&app, UpdatePhase::Checking)?;
    let config = match updater_config(&manager) {
        Ok(config) => config,
        Err(code) => return fail(&manager, &app, &code),
    };
    let update = match fetch_update(&app, &config).await {
        Ok(update) => update,
        Err(code) => return fail(&manager, &app, &code),
    };
    let Some(update) = update else {
        return manager.set_candidate(&app, None);
    };
    let current_version = manager.snapshot()?.current_version;
    match candidate_from_update(&config, &current_version, &update) {
        Ok(candidate) => manager.set_candidate(&app, Some(candidate)),
        Err(code) => fail(&manager, &app, &code),
    }
}

#[tauri::command]
pub async fn native_updater_install(
    identity: String,
    app: AppHandle,
    manager: State<'_, Arc<NativeUpdater>>,
) -> Result<UpdateSnapshot, String> {
    if identity.len() != 64 || !identity.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return fail(&manager, &app, "update_identity_invalid");
    }
    let expected = match manager.expected_identity() {
        Ok(expected) => expected,
        Err(code) => return fail(&manager, &app, &code),
    };
    if identity != expected {
        return fail(&manager, &app, "update_identity_changed");
    }
    manager.begin(&app, UpdatePhase::Downloading)?;
    let config = match updater_config(&manager) {
        Ok(config) => config,
        Err(code) => return fail(&manager, &app, &code),
    };
    let (cancel_sender, mut cancel_receiver) = watch::channel(false);
    manager.install_cancel(cancel_sender)?;
    let fetch = fetch_update(&app, &config);
    tokio::pin!(fetch);
    let update = loop {
        tokio::select! {
            result = &mut fetch => break match result {
                Ok(Some(update)) => update,
                Ok(None) => return fail(&manager, &app, "update_candidate_missing"),
                Err(code) => return fail(&manager, &app, &code),
            },
            changed = cancel_receiver.changed() => {
                if changed.is_ok() && *cancel_receiver.borrow() {
                    manager.clear_cancel();
                    return manager.set_phase(&app, UpdatePhase::Cancelled, None);
                }
            }
        }
    };
    let current_version = manager.snapshot()?.current_version;
    let candidate = match candidate_from_update(&config, &current_version, &update) {
        Ok(candidate) => candidate,
        Err(code) => return fail(&manager, &app, &code),
    };
    if candidate.identity != expected {
        return fail(&manager, &app, "update_identity_changed");
    }

    let progress_manager = Arc::clone(&manager);
    let progress_app = app.clone();
    let download = update.download(
        move |chunk, total| progress_manager.progress(&progress_app, chunk, total),
        || {},
    );
    tokio::pin!(download);
    let bytes = loop {
        tokio::select! {
            result = &mut download => break match result {
                Ok(bytes) => bytes,
                Err(_) => return fail(&manager, &app, "update_download_failed"),
            },
            changed = cancel_receiver.changed() => {
                if changed.is_ok() && *cancel_receiver.borrow() {
                    manager.clear_cancel();
                    return manager.set_phase(&app, UpdatePhase::Cancelled, None);
                }
            }
        }
    };
    manager.clear_cancel();
    manager.set_phase(&app, UpdatePhase::Installing, None)?;
    if update.install(bytes).is_err() {
        return fail(&manager, &app, "update_install_failed");
    }
    manager.set_phase(&app, UpdatePhase::RestartRequired, None)
}

#[tauri::command]
pub fn native_updater_cancel(manager: State<'_, Arc<NativeUpdater>>) -> Result<bool, String> {
    manager.cancel_download()
}

#[tauri::command]
pub fn native_updater_restart(
    app: AppHandle,
    manager: State<'_, Arc<NativeUpdater>>,
) -> Result<(), String> {
    if manager.snapshot()?.phase != UpdatePhase::RestartRequired {
        return Err("update_restart_not_ready".to_string());
    }
    app.restart();
}

#[cfg(test)]
mod tests {
    use super::*;

    const TEST_PUBLIC_KEY: &str = "untrusted comment: minisign public key\nRWQ00000000000000000000000000000000000000000000000000000000000";

    #[test]
    fn configuration_requires_a_complete_https_channel() {
        assert!(UpdaterConfig::parse(None, None, None).unwrap().is_none());
        assert!(
            UpdaterConfig::parse(
                Some("stable"),
                Some("https://updates.example.test/stable/latest.json"),
                Some(TEST_PUBLIC_KEY),
            )
            .unwrap()
            .is_some()
        );
        assert!(
            UpdaterConfig::parse(
                Some("nightly"),
                Some("https://updates.example.test"),
                Some(TEST_PUBLIC_KEY),
            )
            .is_err()
        );
        assert!(
            UpdaterConfig::parse(
                Some("stable"),
                Some("http://updates.example.test"),
                Some(TEST_PUBLIC_KEY),
            )
            .is_err()
        );
        assert!(
            UpdaterConfig::parse(
                Some("stable"),
                Some("https://user@updates.example.test"),
                Some(TEST_PUBLIC_KEY),
            )
            .is_err()
        );
        assert!(UpdaterConfig::parse(Some("stable"), None, Some(TEST_PUBLIC_KEY)).is_err());
        assert!(UpdaterConfig::parse(Some("stable"), None, None).is_err());
    }

    #[test]
    fn channels_reject_prerelease_leakage() {
        assert!(UpdateChannel::Stable.accepts(&Version::parse("1.2.3").unwrap()));
        assert!(!UpdateChannel::Stable.accepts(&Version::parse("1.2.3-beta.1").unwrap()));
        assert!(UpdateChannel::Beta.accepts(&Version::parse("1.2.3-beta.1").unwrap()));
        assert!(!UpdateChannel::Beta.accepts(&Version::parse("1.2.3-rc.1").unwrap()));
        assert!(!UpdateChannel::Beta.accepts(&Version::parse("1.2.3").unwrap()));
    }

    #[test]
    fn candidate_policy_accepts_only_the_expected_newer_release() {
        let config = UpdaterConfig::parse(
            Some("stable"),
            Some("https://updates.example.test/stable/latest.json"),
            Some(TEST_PUBLIC_KEY),
        )
        .unwrap()
        .unwrap();
        let metadata = MailflowManifest {
            channel: UpdateChannel::Stable,
            bundle_identifier: BUNDLE_IDENTIFIER.to_string(),
            target: UPDATE_TARGET.to_string(),
        };
        let download = Url::parse("https://updates.example.test/Mailflow.app.tar.gz").unwrap();
        assert!(
            validate_candidate_policy(
                &config,
                "1.0.0",
                "1.1.0",
                UPDATE_TARGET,
                &download,
                &metadata,
                "Safe notes\nwith two lines",
            )
            .is_ok()
        );

        let cases = [
            (
                "1.0.0",
                UPDATE_TARGET,
                &metadata,
                "update_downgrade_rejected",
            ),
            (
                "1.1.0-beta.1",
                UPDATE_TARGET,
                &metadata,
                "update_wrong_channel",
            ),
            ("1.1.0", "darwin-aarch64", &metadata, "update_incompatible"),
        ];
        for (version, target, manifest, expected) in cases {
            assert_eq!(
                validate_candidate_policy(
                    &config,
                    "1.0.0",
                    version,
                    target,
                    &download,
                    manifest,
                    "Safe notes",
                )
                .unwrap_err(),
                expected
            );
        }

        let wrong_identity = MailflowManifest {
            bundle_identifier: "dev.example.other".to_string(),
            ..metadata
        };
        assert_eq!(
            validate_candidate_policy(
                &config,
                "1.0.0",
                "1.1.0",
                UPDATE_TARGET,
                &download,
                &wrong_identity,
                "Safe notes",
            )
            .unwrap_err(),
            "update_incompatible"
        );

        let wrong_channel = MailflowManifest {
            channel: UpdateChannel::Beta,
            bundle_identifier: BUNDLE_IDENTIFIER.to_string(),
            target: UPDATE_TARGET.to_string(),
        };
        assert_eq!(
            validate_candidate_policy(
                &config,
                "1.0.0",
                "1.1.0",
                UPDATE_TARGET,
                &download,
                &wrong_channel,
                "Safe notes",
            )
            .unwrap_err(),
            "update_wrong_channel"
        );

        assert_eq!(
            validate_candidate_policy(
                &config,
                "1.0.0",
                "1.1.0",
                UPDATE_TARGET,
                &download,
                &MailflowManifest {
                    channel: UpdateChannel::Stable,
                    bundle_identifier: BUNDLE_IDENTIFIER.to_string(),
                    target: UPDATE_TARGET.to_string(),
                },
                &"x".repeat(MAX_NOTES_CHARS + 1),
            )
            .unwrap_err(),
            "update_manifest_invalid"
        );
    }

    #[test]
    fn release_identity_changes_for_any_signed_release_component() {
        let base = release_identity(
            UpdateChannel::Stable,
            "1.2.3",
            "https://updates.example.test/mailflow.tar.gz",
            "signature-one",
            UPDATE_TARGET,
        );
        for changed in [
            release_identity(
                UpdateChannel::Beta,
                "1.2.3",
                "https://updates.example.test/mailflow.tar.gz",
                "signature-one",
                UPDATE_TARGET,
            ),
            release_identity(
                UpdateChannel::Stable,
                "1.2.4",
                "https://updates.example.test/mailflow.tar.gz",
                "signature-one",
                UPDATE_TARGET,
            ),
            release_identity(
                UpdateChannel::Stable,
                "1.2.3",
                "https://updates.example.test/other.tar.gz",
                "signature-one",
                UPDATE_TARGET,
            ),
            release_identity(
                UpdateChannel::Stable,
                "1.2.3",
                "https://updates.example.test/mailflow.tar.gz",
                "signature-two",
                UPDATE_TARGET,
            ),
            release_identity(
                UpdateChannel::Stable,
                "1.2.3",
                "https://updates.example.test/mailflow.tar.gz",
                "signature-one",
                "darwin-aarch64",
            ),
        ] {
            assert_ne!(base, changed);
        }
    }

    #[test]
    fn cancellation_signal_is_scoped_to_the_active_download() {
        let manager = NativeUpdater {
            config: None,
            state: Mutex::new(UpdateState {
                snapshot: UpdateSnapshot::default(),
                candidate_identity: None,
            }),
            cancel: Mutex::new(None),
        };
        assert!(!manager.cancel_download().unwrap());
        let (sender, receiver) = watch::channel(false);
        manager.install_cancel(sender).unwrap();
        assert!(manager.cancel_download().unwrap());
        assert!(*receiver.borrow());
        manager.clear_cancel();
        assert!(!manager.cancel_download().unwrap());
    }
}
