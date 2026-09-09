use crate::{
    configured_origin,
    offline_cache::{CacheKind, OfflineCache},
    same_origin,
};
use serde_json::Value;
use std::sync::Arc;
use tauri::WebviewWindow;

pub(crate) fn authorize(window: &WebviewWindow) -> Result<(), String> {
    let configured = configured_origin().map_err(|_| "desktop origin unavailable".to_owned())?;
    let current = window
        .url()
        .map_err(|_| "desktop origin unavailable".to_owned())?;
    if same_origin(&current, &configured) {
        Ok(())
    } else {
        Err("offline cache access denied".to_owned())
    }
}

#[tauri::command]
pub async fn offline_cache_has_accounts(
    window: WebviewWindow,
    cache: tauri::State<'_, Arc<OfflineCache>>,
) -> Result<bool, String> {
    authorize(&window)?;
    let cache = Arc::clone(cache.inner());
    tauri::async_runtime::spawn_blocking(move || {
        cache.has_accounts().map_err(|error| error.to_string())
    })
    .await
    .map_err(|_| "offline cache task failed".to_owned())?
}

#[tauri::command]
pub async fn offline_cache_list_accounts(
    window: WebviewWindow,
    cache: tauri::State<'_, Arc<OfflineCache>>,
) -> Result<Vec<Value>, String> {
    authorize(&window)?;
    let cache = Arc::clone(cache.inner());
    tauri::async_runtime::spawn_blocking(move || {
        cache.list_accounts().map_err(|error| error.to_string())
    })
    .await
    .map_err(|_| "offline cache task failed".to_owned())?
}

#[tauri::command]
pub async fn offline_cache_store_accounts(
    window: WebviewWindow,
    cache: tauri::State<'_, Arc<OfflineCache>>,
    accounts: Vec<Value>,
) -> Result<(), String> {
    authorize(&window)?;
    let cache = Arc::clone(cache.inner());
    tauri::async_runtime::spawn_blocking(move || {
        cache
            .store_accounts(accounts)
            .map_err(|error| error.to_string())
    })
    .await
    .map_err(|_| "offline cache task failed".to_owned())?
}

#[tauri::command]
pub async fn offline_cache_write(
    window: WebviewWindow,
    cache: tauri::State<'_, Arc<OfflineCache>>,
    account_id: String,
    kind: String,
    cache_key: String,
    value: Value,
) -> Result<(), String> {
    authorize(&window)?;
    let kind = CacheKind::parse(&kind).ok_or_else(|| "offline cache kind is invalid".to_owned())?;
    let cache = Arc::clone(cache.inner());
    tauri::async_runtime::spawn_blocking(move || {
        cache
            .write(&account_id, kind, &cache_key, &value)
            .map_err(|error| error.to_string())
    })
    .await
    .map_err(|_| "offline cache task failed".to_owned())?
}

#[tauri::command]
pub async fn offline_cache_read(
    window: WebviewWindow,
    cache: tauri::State<'_, Arc<OfflineCache>>,
    account_id: String,
    kind: String,
    cache_key: String,
) -> Result<Option<Value>, String> {
    authorize(&window)?;
    let kind = CacheKind::parse(&kind).ok_or_else(|| "offline cache kind is invalid".to_owned())?;
    let cache = Arc::clone(cache.inner());
    tauri::async_runtime::spawn_blocking(move || {
        cache
            .read(&account_id, kind, &cache_key)
            .map_err(|error| error.to_string())
    })
    .await
    .map_err(|_| "offline cache task failed".to_owned())?
}

#[tauri::command]
pub async fn offline_cache_remove_account(
    window: WebviewWindow,
    cache: tauri::State<'_, Arc<OfflineCache>>,
    account_id: String,
) -> Result<(), String> {
    authorize(&window)?;
    let cache = Arc::clone(cache.inner());
    tauri::async_runtime::spawn_blocking(move || {
        cache
            .remove_account(&account_id)
            .map_err(|error| error.to_string())
    })
    .await
    .map_err(|_| "offline cache task failed".to_owned())?
}
