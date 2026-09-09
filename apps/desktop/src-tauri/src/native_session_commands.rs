use crate::{
    configured_origin,
    native_session::{NativeSession, NativeSessionManager},
    offline_cache::OfflineCache,
    offline_commands::authorize,
};
use std::sync::Arc;
use tauri::WebviewWindow;

#[tauri::command]
pub async fn native_session_activate(
    window: WebviewWindow,
    sessions: tauri::State<'_, Arc<NativeSessionManager>>,
    session_token: String,
) -> Result<NativeSession, String> {
    authorize(&window)?;
    sessions
        .activate(session_token)
        .await
        .map_err(|error| error.to_string())
}

#[tauri::command]
pub async fn native_session_current(
    window: WebviewWindow,
    sessions: tauri::State<'_, Arc<NativeSessionManager>>,
) -> Result<Option<NativeSession>, String> {
    authorize(&window)?;
    sessions.current().await.map_err(|error| error.to_string())
}

#[tauri::command]
pub async fn native_session_identity(
    window: WebviewWindow,
    sessions: tauri::State<'_, Arc<NativeSessionManager>>,
) -> Result<String, String> {
    authorize(&window)?;
    sessions.identity().await.map_err(|error| error.to_string())
}

#[tauri::command]
pub async fn native_passkey_begin(
    window: WebviewWindow,
    sessions: tauri::State<'_, Arc<NativeSessionManager>>,
    name: String,
) -> Result<serde_json::Value, String> {
    authorize(&window)?;
    sessions
        .begin_passkey(name)
        .await
        .map_err(|error| error.to_string())
}

#[tauri::command]
pub async fn native_passkey_finish(
    window: WebviewWindow,
    sessions: tauri::State<'_, Arc<NativeSessionManager>>,
    name: String,
    response: serde_json::Value,
) -> Result<(), String> {
    authorize(&window)?;
    sessions
        .finish_passkey(name, response)
        .await
        .map_err(|error| error.to_string())
}

#[tauri::command]
pub async fn native_session_logout(
    window: WebviewWindow,
    sessions: tauri::State<'_, Arc<NativeSessionManager>>,
    cache: tauri::State<'_, Arc<OfflineCache>>,
) -> Result<(), String> {
    authorize(&window)?;
    let session_result = sessions.logout().await;
    let cache = Arc::clone(cache.inner());
    let cache_result = tauri::async_runtime::spawn_blocking(move || cache.clear_all())
        .await
        .map_err(|_| "offline cache task failed".to_owned())?;
    session_result.map_err(|error| error.to_string())?;
    cache_result.map_err(|error| error.to_string())
}

#[tauri::command]
pub async fn native_session_forget_local(
    window: WebviewWindow,
    sessions: tauri::State<'_, Arc<NativeSessionManager>>,
    cache: tauri::State<'_, Arc<OfflineCache>>,
) -> Result<(), String> {
    authorize(&window)?;
    sessions
        .forget_local()
        .await
        .map_err(|error| error.to_string())?;
    let cache = Arc::clone(cache.inner());
    tauri::async_runtime::spawn_blocking(move || cache.clear_all())
        .await
        .map_err(|_| "offline cache task failed".to_owned())?
        .map_err(|error| error.to_string())
}

pub fn manager() -> Result<Arc<NativeSessionManager>, String> {
    let origin = configured_origin().map_err(|error| error.to_string())?;
    NativeSessionManager::new(origin)
        .map(Arc::new)
        .map_err(|error| error.to_string())
}
