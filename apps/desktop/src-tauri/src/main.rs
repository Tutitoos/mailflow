#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::sync::Arc;
use std::{error::Error, fmt};
use tauri::{
    Emitter, Manager, Url, WebviewUrl, WebviewWindowBuilder,
    menu::{Menu, MenuItemBuilder, SubmenuBuilder},
    webview::NewWindowResponse,
};
use tauri_plugin_deep_link::DeepLinkExt;

mod native_experience;
mod native_session;
mod native_session_commands;
mod native_updater;
mod offline_cache;
mod offline_commands;

const DEV_ORIGIN: &str = "http://127.0.0.1:4310";
const DESKTOP_MARKER: &str = "desktop=1";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct DesktopConfigError(&'static str);

impl fmt::Display for DesktopConfigError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(self.0)
    }
}

impl Error for DesktopConfigError {}

fn main() {
    let mut builder = tauri::Builder::default();

    #[cfg(desktop)]
    {
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            focus_main_window(app);
        }));
    }

    builder = builder
        .plugin(tauri_plugin_deep_link::init())
        .plugin(
            tauri_plugin_opener::Builder::new()
                .open_js_links_on_click(false)
                .build(),
        )
        .plugin(tauri_plugin_window_state::Builder::default().build());

    if native_updater::plugin_enabled() {
        builder = builder.plugin(tauri_plugin_updater::Builder::new().build());
    }

    builder
        .invoke_handler(tauri::generate_handler![
            native_session_commands::native_passkey_begin,
            native_session_commands::native_passkey_finish,
            offline_commands::offline_cache_has_accounts,
            offline_commands::offline_cache_list_accounts,
            offline_commands::offline_cache_read,
            offline_commands::offline_cache_remove_account,
            offline_commands::offline_cache_store_accounts,
            offline_commands::offline_cache_write,
            native_session_commands::native_session_activate,
            native_session_commands::native_session_current,
            native_session_commands::native_session_forget_local,
            native_session_commands::native_session_identity,
            native_session_commands::native_session_logout,
            native_experience::native_experience_state,
            native_experience::native_notifications_enable,
            native_experience::native_notifications_set_privacy,
            native_experience::native_notify_new_mail,
            native_experience::native_notification_take_pending,
            native_experience::native_set_unread_badge,
            native_updater::native_updater_cancel,
            native_updater::native_updater_check,
            native_updater::native_updater_install,
            native_updater::native_updater_restart,
            native_updater::native_updater_state,
        ])
        .setup(setup)
        .run(tauri::generate_context!())
        .expect("failed to run Mailflow desktop");
}

fn setup(app: &mut tauri::App) -> Result<(), Box<dyn Error>> {
    let cache_directory = app.path().app_local_data_dir()?;
    app.manage(Arc::new(offline_cache::OfflineCache::new(
        cache_directory.join("offline-cache.sqlite3"),
    )));
    app.manage(native_session_commands::manager()?);
    app.manage(Arc::new(native_updater::NativeUpdater::load(
        app.package_info().version.to_string(),
    )));
    app.manage(native_experience::NativeExperience::load(
        cache_directory.join("native-experience.json"),
    )?);
    native_experience::install_notification_delegate(app.handle());
    install_native_menu(app)?;
    let origin = configured_origin()?;
    let initial_url = app
        .deep_link()
        .get_current()?
        .and_then(|urls| first_deep_link_target(&origin, urls))
        .unwrap_or_else(|| desktop_entry_url(&origin));

    let navigation_origin = origin.clone();
    WebviewWindowBuilder::new(app, "main", WebviewUrl::External(initial_url))
        .title("Mailflow")
        .inner_size(1440.0, 900.0)
        .min_inner_size(900.0, 620.0)
        .resizable(true)
        .on_navigation(move |url| {
            if same_origin(url, &navigation_origin) {
                return true;
            }
            open_external(url);
            false
        })
        .on_new_window(|url, _features| {
            open_external(&url);
            NewWindowResponse::Deny
        })
        .build()?;

    let app_handle = app.handle().clone();
    app.deep_link().on_open_url(move |event| {
        if let Some(target) = first_deep_link_target(&origin, event.urls()) {
            if let Some(window) = app_handle.get_webview_window("main") {
                let _ = window.navigate(target);
                let _ = window.show();
                let _ = window.set_focus();
            }
        }
    });

    Ok(())
}

fn install_native_menu(app: &tauri::App) -> tauri::Result<()> {
    let new_message = MenuItemBuilder::with_id("mailflow.compose", "New Message")
        .accelerator("CmdOrCtrl+N")
        .build(app)?;
    let search = MenuItemBuilder::with_id("mailflow.search", "Search Mail")
        .accelerator("CmdOrCtrl+K")
        .build(app)?;
    let inbox = MenuItemBuilder::with_id("mailflow.inbox", "Inbox")
        .accelerator("CmdOrCtrl+1")
        .build(app)?;
    let refresh = MenuItemBuilder::with_id("mailflow.refresh", "Refresh")
        .accelerator("CmdOrCtrl+R")
        .build(app)?;
    let settings = MenuItemBuilder::with_id("mailflow.settings", "Settings")
        .accelerator("CmdOrCtrl+Comma")
        .build(app)?;
    let mailbox = SubmenuBuilder::new(app, "Mailbox")
        .items(&[&new_message, &search, &inbox, &refresh])
        .separator()
        .item(&settings)
        .build()?;
    let menu = Menu::default(app.handle())?;
    menu.insert(&mailbox, 1)?;
    app.set_menu(menu)?;
    app.on_menu_event(|app_handle, event| {
        let command = native_menu_command(event.id().as_ref());
        if let Some(command) = command {
            focus_main_window(app_handle);
            let _ = app_handle.emit(
                "mailflow:native-command",
                serde_json::json!({ "command": command }),
            );
        }
    });
    Ok(())
}

fn native_menu_command(id: &str) -> Option<&'static str> {
    match id {
        "mailflow.compose" => Some("compose"),
        "mailflow.search" => Some("search"),
        "mailflow.inbox" => Some("inbox"),
        "mailflow.refresh" => Some("refresh"),
        "mailflow.settings" => Some("settings"),
        _ => None,
    }
}

fn configured_origin() -> Result<Url, DesktopConfigError> {
    if cfg!(debug_assertions) {
        return parse_server_origin(
            option_env!("MAILFLOW_DESKTOP_DEV_ORIGIN").unwrap_or(DEV_ORIGIN),
            true,
        );
    }
    let origin = option_env!("MAILFLOW_DESKTOP_ORIGIN").ok_or(DesktopConfigError(
        "MAILFLOW_DESKTOP_ORIGIN must be set to the installation HTTPS origin at build time",
    ))?;
    parse_server_origin(origin, false)
}

fn parse_server_origin(raw: &str, allow_loopback_http: bool) -> Result<Url, DesktopConfigError> {
    let url = Url::parse(raw.trim())
        .map_err(|_| DesktopConfigError("the Mailflow server origin is invalid"))?;
    let loopback_http = allow_loopback_http
        && url.scheme() == "http"
        && matches!(url.host_str(), Some("localhost" | "127.0.0.1" | "::1"));
    if url.scheme() != "https" && !loopback_http {
        return Err(DesktopConfigError(
            "the Mailflow server origin must use HTTPS",
        ));
    }
    if url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(DesktopConfigError(
            "the Mailflow server value must be an origin without credentials, path, query, or fragment",
        ));
    }
    Ok(url)
}

fn desktop_entry_url(origin: &Url) -> Url {
    let mut url = origin.clone();
    url.set_query(Some(DESKTOP_MARKER));
    url
}

fn same_origin(candidate: &Url, origin: &Url) -> bool {
    candidate.scheme() == origin.scheme()
        && candidate.host_str() == origin.host_str()
        && candidate.port_or_known_default() == origin.port_or_known_default()
        && candidate.username().is_empty()
        && candidate.password().is_none()
}

fn open_external(url: &Url) {
    if matches!(url.scheme(), "http" | "https" | "mailto") {
        let _ = tauri_plugin_opener::open_url(url.as_str(), None::<&str>);
    }
}

fn first_deep_link_target(origin: &Url, urls: Vec<Url>) -> Option<Url> {
    urls.into_iter()
        .find_map(|url| deep_link_target(origin, &url))
}

fn deep_link_target(origin: &Url, link: &Url) -> Option<Url> {
    if link.scheme() != "mailflow"
        || link.host_str() != Some("open")
        || !link.username().is_empty()
        || link.password().is_some()
        || link.port().is_some()
        || link.fragment().is_some()
        || !allowed_path(link.path())
        || !allowed_query(link.path(), link)
    {
        return None;
    }

    let mut target = origin.clone();
    target.set_path(link.path());
    target.set_query(link.query());
    Some(target)
}

fn allowed_path(path: &str) -> bool {
    path == "/"
        || path == "/settings/accounts"
        || matches!(
            path.strip_prefix("/admin/"),
            Some(
                "overview"
                    | "accounts"
                    | "synchronization"
                    | "metrics"
                    | "logs"
                    | "errors"
                    | "translations"
                    | "cdn"
                    | "backups"
                    | "alerts"
                    | "updates"
                    | "settings"
            )
        )
        || path == "/admin"
}

fn allowed_query(path: &str, link: &Url) -> bool {
    if link.query().is_none() {
        return true;
    }
    if path != "/settings/accounts" {
        return false;
    }

    let mut provider = None;
    let mut sync = None;
    for (key, value) in link.query_pairs() {
        match key.as_ref() {
            "google" | "microsoft"
                if provider.is_none() && matches!(value.as_ref(), "connected" | "failed") =>
            {
                provider = Some(key.into_owned());
            }
            "sync" if sync.is_none() && value == "pending" => sync = Some(()),
            _ => return false,
        }
    }
    provider.is_some()
}

fn focus_main_window(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.set_focus();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn production_origin_requires_plain_https_origin() {
        assert!(parse_server_origin("https://mail.example.test", false).is_ok());
        assert!(parse_server_origin("http://mail.example.test", false).is_err());
        assert!(parse_server_origin("https://user@mail.example.test", false).is_err());
        assert!(parse_server_origin("https://mail.example.test/path", false).is_err());
        assert!(parse_server_origin("https://mail.example.test?private=value", false).is_err());
    }

    #[test]
    fn development_origin_only_allows_loopback_http() {
        assert!(parse_server_origin("http://127.0.0.1:4310", true).is_ok());
        assert!(parse_server_origin("http://localhost:4310", true).is_ok());
        assert!(parse_server_origin("http://192.0.2.1:4310", true).is_err());
    }

    #[test]
    fn navigation_allows_only_the_configured_origin() {
        let origin = Url::parse("https://mail.example.test/").unwrap();
        assert!(same_origin(
            &Url::parse("https://mail.example.test/admin/metrics").unwrap(),
            &origin
        ));
        assert!(!same_origin(
            &Url::parse("https://login.example.test/").unwrap(),
            &origin
        ));
        assert!(!same_origin(
            &Url::parse("http://mail.example.test/").unwrap(),
            &origin
        ));
    }

    #[test]
    fn deep_links_are_bounded_and_never_carry_mail_queries() {
        let origin = Url::parse("https://mail.example.test/").unwrap();
        let valid =
            Url::parse("mailflow://open/settings/accounts?google=connected&sync=pending").unwrap();
        assert_eq!(
            deep_link_target(&origin, &valid).unwrap().as_str(),
            "https://mail.example.test/settings/accounts?google=connected&sync=pending"
        );

        for invalid in [
            "mailflow://open/?q=private-subject",
            "mailflow://open/settings/accounts?next=https://example.test",
            "mailflow://open/admin/private",
            "mailflow://other/settings/accounts?google=connected",
            "https://mail.example.test/settings/accounts?google=connected",
        ] {
            assert!(deep_link_target(&origin, &Url::parse(invalid).unwrap()).is_none());
        }
    }

    #[test]
    fn native_menu_ids_map_only_to_supported_web_commands() {
        assert_eq!(native_menu_command("mailflow.compose"), Some("compose"));
        assert_eq!(native_menu_command("mailflow.search"), Some("search"));
        assert_eq!(native_menu_command("mailflow.inbox"), Some("inbox"));
        assert_eq!(native_menu_command("mailflow.refresh"), Some("refresh"));
        assert_eq!(native_menu_command("mailflow.settings"), Some("settings"));
        assert_eq!(native_menu_command("mailflow.private"), None);
    }
}
