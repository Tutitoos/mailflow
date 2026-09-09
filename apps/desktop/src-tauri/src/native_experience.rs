use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    collections::VecDeque,
    fs, io,
    path::{Path, PathBuf},
    sync::Mutex,
};
use tauri::{AppHandle, State};

const MAX_RECENT_EVENTS: usize = 256;
const MAX_EVENT_ID: usize = 256;
const MAX_PREVIEW: usize = 160;

#[derive(Clone, Copy, Debug, Default, Deserialize, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum NotificationPrivacy {
    #[default]
    Hidden,
    Sender,
    Full,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(default, deny_unknown_fields)]
struct StoredExperience {
    notifications_enabled: bool,
    privacy: NotificationPrivacy,
    recent_event_hashes: VecDeque<String>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum PermissionState {
    NotDetermined,
    Denied,
    Authorized,
    Unsupported,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ExperienceState {
    notifications_enabled: bool,
    privacy: NotificationPrivacy,
    permission: PermissionState,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct NewMailNotification {
    event_id: String,
    account_id: String,
    thread_id: String,
    sender: String,
    subject: String,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum NotificationOutcome {
    Shown,
    Duplicate,
    Disabled,
    PermissionDenied,
    Unsupported,
}

pub struct NativeExperience {
    path: PathBuf,
    stored: Mutex<StoredExperience>,
    pending_target: Mutex<Option<NotificationTarget>>,
}

impl NativeExperience {
    pub fn load(path: PathBuf) -> io::Result<Self> {
        let stored = match fs::read(&path) {
            Ok(bytes) => serde_json::from_slice::<StoredExperience>(&bytes).unwrap_or_default(),
            Err(error) if error.kind() == io::ErrorKind::NotFound => StoredExperience::default(),
            Err(error) => return Err(error),
        };
        Ok(Self {
            path,
            stored: Mutex::new(stored),
            pending_target: Mutex::new(None),
        })
    }

    fn persist(&self, stored: &StoredExperience) -> Result<(), String> {
        let parent = self
            .path
            .parent()
            .ok_or_else(|| "native settings path has no parent".to_string())?;
        fs::create_dir_all(parent).map_err(|_| "cannot create native settings directory")?;
        let temporary = self.path.with_extension("json.tmp");
        let bytes = serde_json::to_vec(stored).map_err(|_| "cannot encode native settings")?;
        write_private_file(&temporary, &bytes)
            .map_err(|_| "cannot write native settings".to_string())?;
        fs::rename(&temporary, &self.path).map_err(|_| "cannot commit native settings".to_string())
    }

    fn set_enabled(&self, enabled: bool) -> Result<(), String> {
        let mut stored = self
            .stored
            .lock()
            .map_err(|_| "native settings unavailable")?;
        stored.notifications_enabled = enabled;
        self.persist(&stored)
    }

    fn set_privacy(&self, privacy: NotificationPrivacy) -> Result<(), String> {
        let mut stored = self
            .stored
            .lock()
            .map_err(|_| "native settings unavailable")?;
        stored.privacy = privacy;
        self.persist(&stored)
    }

    fn snapshot(&self, permission: PermissionState) -> Result<ExperienceState, String> {
        let stored = self
            .stored
            .lock()
            .map_err(|_| "native settings unavailable")?;
        Ok(ExperienceState {
            notifications_enabled: stored.notifications_enabled
                && permission == PermissionState::Authorized,
            privacy: stored.privacy,
            permission,
        })
    }

    fn prepare_notification(
        &self,
        input: NewMailNotification,
    ) -> Result<PreparedNotification, String> {
        validate_identifier(&input.event_id, MAX_EVENT_ID, "event")?;
        validate_resource_id(&input.account_id, "account")?;
        validate_resource_id(&input.thread_id, "thread")?;

        let mut stored = self
            .stored
            .lock()
            .map_err(|_| "native settings unavailable")?;
        if !stored.notifications_enabled {
            return Ok(PreparedNotification::disabled());
        }
        let event_hash = hex::encode(Sha256::digest(input.event_id.as_bytes()));
        if stored.recent_event_hashes.contains(&event_hash) {
            return Ok(PreparedNotification::duplicate());
        }
        let sender = bounded_preview(&input.sender);
        let subject = bounded_preview(&input.subject);
        let (title, body) = notification_copy(stored.privacy, &sender, &subject);
        stored.recent_event_hashes.push_back(event_hash);
        while stored.recent_event_hashes.len() > MAX_RECENT_EVENTS {
            stored.recent_event_hashes.pop_front();
        }
        self.persist(&stored)?;
        Ok(PreparedNotification {
            outcome: NotificationOutcome::Shown,
            title,
            body,
            account_id: input.account_id,
            thread_id: input.thread_id,
        })
    }

    fn queue_target(&self, account_id: String, thread_id: String) {
        if let Ok(mut target) = self.pending_target.lock() {
            *target = Some(NotificationTarget {
                account_id,
                thread_id,
            });
        }
    }

    fn take_target(&self) -> Result<Option<NotificationTarget>, String> {
        self.pending_target
            .lock()
            .map(|mut target| target.take())
            .map_err(|_| "notification target unavailable".to_string())
    }
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NotificationTarget {
    account_id: String,
    thread_id: String,
}

struct PreparedNotification {
    outcome: NotificationOutcome,
    title: String,
    body: String,
    account_id: String,
    thread_id: String,
}

impl PreparedNotification {
    fn disabled() -> Self {
        Self::without_payload(NotificationOutcome::Disabled)
    }

    fn duplicate() -> Self {
        Self::without_payload(NotificationOutcome::Duplicate)
    }

    fn without_payload(outcome: NotificationOutcome) -> Self {
        Self {
            outcome,
            title: String::new(),
            body: String::new(),
            account_id: String::new(),
            thread_id: String::new(),
        }
    }
}

fn notification_copy(
    privacy: NotificationPrivacy,
    sender: &str,
    subject: &str,
) -> (String, String) {
    match privacy {
        NotificationPrivacy::Hidden => (
            "New mail".to_string(),
            "Open Mailflow to view it.".to_string(),
        ),
        NotificationPrivacy::Sender => (
            if sender.is_empty() {
                "New mail"
            } else {
                sender
            }
            .to_string(),
            "Open Mailflow to view the message.".to_string(),
        ),
        NotificationPrivacy::Full => (
            if sender.is_empty() {
                "New mail"
            } else {
                sender
            }
            .to_string(),
            if subject.is_empty() {
                "Open Mailflow to view the message."
            } else {
                subject
            }
            .to_string(),
        ),
    }
}

fn bounded_preview(value: &str) -> String {
    value
        .chars()
        .filter(|character| !character.is_control())
        .take(MAX_PREVIEW)
        .collect::<String>()
        .trim()
        .to_string()
}

fn validate_identifier(value: &str, maximum: usize, label: &str) -> Result<(), String> {
    if value.is_empty()
        || value.len() > maximum
        || !value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b':' | b'.' | b'+')
        })
    {
        return Err(format!("invalid {label} identifier"));
    }
    Ok(())
}

fn validate_resource_id(value: &str, label: &str) -> Result<(), String> {
    if value.len() != 36
        || !value.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        })
    {
        return Err(format!("invalid {label} identifier"));
    }
    Ok(())
}

fn parse_notification_target(target: &str) -> Option<(String, String)> {
    let (account_id, thread_id) = target.split_once(':')?;
    validate_resource_id(account_id, "account").ok()?;
    validate_resource_id(thread_id, "thread").ok()?;
    Some((account_id.to_string(), thread_id.to_string()))
}

fn validate_badge(count: u32) -> Result<(), String> {
    if count > 999_999 {
        return Err("unread badge exceeds the supported range".to_string());
    }
    Ok(())
}

fn write_private_file(path: &Path, bytes: &[u8]) -> io::Result<()> {
    use std::io::Write;
    let mut options = fs::OpenOptions::new();
    options.create(true).truncate(true).write(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(path)?;
    file.write_all(bytes)?;
    file.sync_all()
}

#[tauri::command]
pub async fn native_experience_state(
    experience: State<'_, NativeExperience>,
) -> Result<ExperienceState, String> {
    experience.snapshot(platform::permission_state().await)
}

#[tauri::command]
pub async fn native_notifications_enable(
    enabled: bool,
    experience: State<'_, NativeExperience>,
) -> Result<ExperienceState, String> {
    let permission = if enabled {
        platform::request_permission().await
    } else {
        platform::permission_state().await
    };
    let should_enable = enabled && permission == PermissionState::Authorized;
    experience.set_enabled(should_enable)?;
    experience.snapshot(permission)
}

#[tauri::command]
pub async fn native_notifications_set_privacy(
    privacy: NotificationPrivacy,
    experience: State<'_, NativeExperience>,
) -> Result<ExperienceState, String> {
    experience.set_privacy(privacy)?;
    experience.snapshot(platform::permission_state().await)
}

#[tauri::command]
pub async fn native_notify_new_mail(
    input: NewMailNotification,
    experience: State<'_, NativeExperience>,
) -> Result<NotificationOutcome, String> {
    let permission = platform::permission_state().await;
    if permission == PermissionState::Unsupported {
        return Ok(NotificationOutcome::Unsupported);
    }
    if permission != PermissionState::Authorized {
        return Ok(NotificationOutcome::PermissionDenied);
    }
    let prepared = experience.prepare_notification(input)?;
    if prepared.outcome != NotificationOutcome::Shown {
        return Ok(prepared.outcome);
    }
    platform::show(&prepared)?;
    Ok(NotificationOutcome::Shown)
}

#[tauri::command]
pub fn native_set_unread_badge(count: u32) -> Result<(), String> {
    validate_badge(count)?;
    platform::set_badge(count)
}

#[tauri::command]
pub fn native_notification_take_pending(
    experience: State<'_, NativeExperience>,
) -> Result<Option<NotificationTarget>, String> {
    experience.take_target()
}

#[cfg(not(target_os = "macos"))]
mod platform {
    use super::{PermissionState, PreparedNotification};
    use tauri::AppHandle;

    pub fn install_delegate(_app: &AppHandle) {}
    pub async fn permission_state() -> PermissionState {
        PermissionState::Unsupported
    }
    pub async fn request_permission() -> PermissionState {
        PermissionState::Unsupported
    }
    pub fn show(_notification: &PreparedNotification) -> Result<(), String> {
        Ok(())
    }
    pub fn set_badge(_count: u32) -> Result<(), String> {
        Ok(())
    }
}

pub fn install_notification_delegate(app: &AppHandle) {
    platform::install_delegate(app);
}

#[cfg(target_os = "macos")]
mod platform {
    use super::{PermissionState, PreparedNotification};
    use block2::{DynBlock, RcBlock};
    use objc2::{
        MainThreadOnly, define_class, msg_send,
        rc::Retained,
        runtime::{Bool, ProtocolObject},
    };
    use objc2_foundation::{MainThreadMarker, NSError, NSObject, NSObjectProtocol, NSString};
    use objc2_user_notifications::{
        UNAuthorizationOptions, UNAuthorizationStatus, UNMutableNotificationContent,
        UNNotification, UNNotificationPresentationOptions, UNNotificationRequest,
        UNNotificationResponse, UNNotificationSettings, UNUserNotificationCenter,
        UNUserNotificationCenterDelegate,
    };
    use std::{
        ptr,
        sync::{OnceLock, mpsc},
        time::Duration,
    };
    use tauri::{AppHandle, Emitter, Manager};

    static APP_HANDLE: OnceLock<AppHandle> = OnceLock::new();

    #[derive(Default)]
    struct NotificationDelegateIvars;

    define_class!(
        #[unsafe(super = NSObject)]
        #[name = "MailflowNotificationDelegate"]
        #[thread_kind = MainThreadOnly]
        #[ivars = NotificationDelegateIvars]
        struct NotificationDelegate;

        unsafe impl NSObjectProtocol for NotificationDelegate {}

        unsafe impl UNUserNotificationCenterDelegate for NotificationDelegate {
            #[unsafe(method(userNotificationCenter:willPresentNotification:withCompletionHandler:))]
            fn will_present(
                &self,
                _center: &UNUserNotificationCenter,
                _notification: &UNNotification,
                completion: &DynBlock<dyn Fn(UNNotificationPresentationOptions)>,
            ) {
                completion.call((UNNotificationPresentationOptions::Banner
                    | UNNotificationPresentationOptions::List
                    | UNNotificationPresentationOptions::Sound
                    | UNNotificationPresentationOptions::Badge,));
            }

            #[unsafe(method(userNotificationCenter:didReceiveNotificationResponse:withCompletionHandler:))]
            fn did_receive(
                &self,
                _center: &UNUserNotificationCenter,
                response: &UNNotificationResponse,
                completion: &DynBlock<dyn Fn()>,
            ) {
                let request = response.notification().request();
                let content = request.content();
                if let Some(target) = content.targetContentIdentifier() {
                    if let Some((account_id, thread_id)) =
                        super::parse_notification_target(&target.to_string())
                    {
                        if let Some(app) = APP_HANDLE.get() {
                            app.state::<super::NativeExperience>()
                                .queue_target(account_id.clone(), thread_id.clone());
                            if let Some(window) = app.get_webview_window("main") {
                                if let Ok(mut url) = window.url() {
                                    url.set_path("/");
                                    url.set_query(None);
                                    url.set_fragment(None);
                                    let _ = window.navigate(url);
                                }
                                let _ = window.show();
                                let _ = window.set_focus();
                            }
                            let _ = app.emit(
                                "mailflow:notification-open",
                                serde_json::json!({"accountId": account_id, "threadId": thread_id}),
                            );
                        }
                    }
                }
                completion.call(());
            }
        }
    );

    impl NotificationDelegate {
        fn new(mtm: MainThreadMarker) -> Retained<Self> {
            let this = Self::alloc(mtm).set_ivars(NotificationDelegateIvars);
            unsafe { msg_send![super(this), init] }
        }
    }

    pub fn install_delegate(app: &AppHandle) {
        let _ = APP_HANDLE.set(app.clone());
        let Some(mtm) = MainThreadMarker::new() else {
            return;
        };
        let delegate = NotificationDelegate::new(mtm);
        let center = UNUserNotificationCenter::currentNotificationCenter();
        center.setDelegate(Some(ProtocolObject::from_ref(&*delegate)));
        let _ = Retained::into_raw(delegate);
    }

    pub async fn permission_state() -> PermissionState {
        tauri::async_runtime::spawn_blocking(permission_state_blocking)
            .await
            .unwrap_or(PermissionState::Denied)
    }

    fn permission_state_blocking() -> PermissionState {
        let center = UNUserNotificationCenter::currentNotificationCenter();
        let (sender, receiver) = mpsc::channel();
        let sender = std::sync::Mutex::new(Some(sender));
        let completion = RcBlock::new(move |settings: ptr::NonNull<UNNotificationSettings>| {
            let status = unsafe { settings.as_ref() }.authorizationStatus();
            if let Ok(mut sender) = sender.lock() {
                if let Some(sender) = sender.take() {
                    let _ = sender.send(map_status(status));
                }
            }
        });
        center.getNotificationSettingsWithCompletionHandler(&completion);
        receiver
            .recv_timeout(Duration::from_secs(5))
            .unwrap_or(PermissionState::Denied)
    }

    pub async fn request_permission() -> PermissionState {
        tauri::async_runtime::spawn_blocking(request_permission_blocking)
            .await
            .unwrap_or(PermissionState::Denied)
    }

    fn request_permission_blocking() -> PermissionState {
        let center = UNUserNotificationCenter::currentNotificationCenter();
        let (sender, receiver) = mpsc::channel();
        let sender = std::sync::Mutex::new(Some(sender));
        let completion = RcBlock::new(move |granted: Bool, _error: *mut NSError| {
            if let Ok(mut sender) = sender.lock() {
                if let Some(sender) = sender.take() {
                    let _ = sender.send(if granted.as_bool() {
                        PermissionState::Authorized
                    } else {
                        PermissionState::Denied
                    });
                }
            }
        });
        center.requestAuthorizationWithOptions_completionHandler(
            UNAuthorizationOptions::Alert
                | UNAuthorizationOptions::Badge
                | UNAuthorizationOptions::Sound,
            &completion,
        );
        receiver
            .recv_timeout(Duration::from_secs(30))
            .unwrap_or(PermissionState::Denied)
    }

    pub fn show(notification: &PreparedNotification) -> Result<(), String> {
        let content = UNMutableNotificationContent::new();
        content.setTitle(&NSString::from_str(&notification.title));
        content.setBody(&NSString::from_str(&notification.body));
        let target = format!("{}:{}", notification.account_id, notification.thread_id);
        content.setTargetContentIdentifier(Some(&NSString::from_str(&target)));
        content.setThreadIdentifier(&NSString::from_str(&notification.account_id));
        let request = UNNotificationRequest::requestWithIdentifier_content_trigger(
            &NSString::from_str(&format!("mailflow-{}", short_hash(&target))),
            &content,
            None,
        );
        UNUserNotificationCenter::currentNotificationCenter()
            .addNotificationRequest_withCompletionHandler(&request, None);
        Ok(())
    }

    pub fn set_badge(count: u32) -> Result<(), String> {
        UNUserNotificationCenter::currentNotificationCenter()
            .setBadgeCount_withCompletionHandler(count as isize, None);
        Ok(())
    }

    fn map_status(status: UNAuthorizationStatus) -> PermissionState {
        match status {
            UNAuthorizationStatus::NotDetermined => PermissionState::NotDetermined,
            UNAuthorizationStatus::Denied => PermissionState::Denied,
            UNAuthorizationStatus::Authorized
            | UNAuthorizationStatus::Provisional
            | UNAuthorizationStatus::Ephemeral => PermissionState::Authorized,
            _ => PermissionState::Denied,
        }
    }

    fn short_hash(value: &str) -> String {
        use sha2::{Digest, Sha256};
        hex::encode(Sha256::digest(value.as_bytes()))[..24].to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn input(event: &str) -> NewMailNotification {
        NewMailNotification {
            event_id: event.to_string(),
            account_id: "018f6e6b-7c11-7d2c-8ce8-e8a98ef77b22".to_string(),
            thread_id: "018f6e6b-7c11-7d2c-8ce8-e8a98ef77b23".to_string(),
            sender: "Sender\nName".to_string(),
            subject: "Private subject".to_string(),
        }
    }

    #[test]
    fn hidden_is_the_default_and_never_exposes_preview_text() {
        assert_eq!(NotificationPrivacy::default(), NotificationPrivacy::Hidden);
        assert_eq!(
            notification_copy(NotificationPrivacy::Hidden, "Sender", "Subject"),
            (
                "New mail".to_string(),
                "Open Mailflow to view it.".to_string()
            )
        );
    }

    #[test]
    fn revoked_permission_disables_the_exposed_notification_state() {
        let directory = tempfile::tempdir().unwrap();
        let experience =
            NativeExperience::load(directory.path().join("native-experience.json")).unwrap();
        experience.set_enabled(true).unwrap();
        assert!(
            !experience
                .snapshot(PermissionState::Denied)
                .unwrap()
                .notifications_enabled
        );
    }

    #[test]
    fn preview_modes_are_bounded_and_strip_control_characters() {
        assert_eq!(bounded_preview("Sender\nName"), "SenderName");
        assert_eq!(
            notification_copy(NotificationPrivacy::Sender, "Sender", "Subject"),
            (
                "Sender".to_string(),
                "Open Mailflow to view the message.".to_string()
            )
        );
        assert_eq!(
            notification_copy(NotificationPrivacy::Full, "Sender", "Subject"),
            ("Sender".to_string(), "Subject".to_string())
        );
    }

    #[test]
    fn duplicate_events_are_suppressed_across_reloads() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("native-experience.json");
        let experience = NativeExperience::load(path.clone()).unwrap();
        experience.set_enabled(true).unwrap();
        assert_eq!(
            experience
                .prepare_notification(input("event:1"))
                .unwrap()
                .outcome,
            NotificationOutcome::Shown
        );
        assert_eq!(
            NativeExperience::load(path)
                .unwrap()
                .prepare_notification(input("event:1"))
                .unwrap()
                .outcome,
            NotificationOutcome::Duplicate
        );
    }

    #[test]
    fn identifiers_and_badges_are_bounded() {
        assert!(validate_resource_id("private", "account").is_err());
        assert!(validate_identifier("contains space", MAX_EVENT_ID, "event").is_err());
        assert!(validate_identifier("sync:account.thread", MAX_EVENT_ID, "event").is_ok());
        assert!(validate_identifier("2026-09-09T12:30:00+02:00", MAX_EVENT_ID, "event").is_ok());
        assert!(validate_badge(999_999).is_ok());
        assert!(validate_badge(1_000_000).is_err());
    }

    #[test]
    fn notification_targets_require_two_valid_resource_ids() {
        let target = "018f6e6b-7c11-7d2c-8ce8-e8a98ef77b22:018f6e6b-7c11-7d2c-8ce8-e8a98ef77b23";
        assert_eq!(
            parse_notification_target(target),
            Some((
                "018f6e6b-7c11-7d2c-8ce8-e8a98ef77b22".to_string(),
                "018f6e6b-7c11-7d2c-8ce8-e8a98ef77b23".to_string()
            ))
        );
        assert!(parse_notification_target("private:thread").is_none());
        assert!(parse_notification_target(&format!("{target}:extra")).is_none());
    }
}
