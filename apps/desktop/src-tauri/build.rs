use std::{env, fs, path::PathBuf};
use url::Url;

const DEV_ORIGIN: &str = "http://127.0.0.1:4310";

fn main() {
    println!("cargo:rerun-if-env-changed=MAILFLOW_DESKTOP_ORIGIN");
    println!("cargo:rerun-if-changed=capabilities/default.json");
    let capabilities = generate_capabilities();
    let pattern = Box::leak(format!("{}/**/*.json", capabilities.display()).into_boxed_str());
    let attributes = tauri_build::Attributes::new()
        .capabilities_path_pattern(pattern)
        .app_manifest(tauri_build::AppManifest::new());
    tauri_build::try_build(attributes).expect("failed to build Mailflow desktop metadata")
}

fn generate_capabilities() -> PathBuf {
    let origin = capability_origin();
    let directory = PathBuf::from(env::var_os("OUT_DIR").expect("Cargo must provide OUT_DIR"))
        .join("mailflow-capabilities");
    fs::create_dir_all(&directory).expect("failed to create generated capability directory");
    fs::copy("capabilities/default.json", directory.join("default.json"))
        .expect("failed to copy local capability");
    let remote = serde_json::json!({
        "identifier": "remote-cache",
        "description": "Only the configured Mailflow installation can access encrypted offline data and native sessions",
        "windows": ["main"],
        "remote": { "urls": [origin] },
        "permissions": ["allow-offline-cache"]
    });
    fs::write(
        directory.join("remote-cache.json"),
        serde_json::to_vec_pretty(&remote).expect("failed to encode remote capability"),
    )
    .expect("failed to write remote capability");
    directory
}

fn capability_origin() -> String {
    let configured = env::var("MAILFLOW_DESKTOP_ORIGIN").ok();
    let value = configured.as_deref().unwrap_or(DEV_ORIGIN);
    let parsed = Url::parse(value).expect("MAILFLOW_DESKTOP_ORIGIN must be an absolute URL");
    let valid_scheme = if configured.is_some() {
        parsed.scheme() == "https"
    } else {
        parsed.scheme() == "http"
    };
    assert!(
        valid_scheme
            && parsed.username().is_empty()
            && parsed.password().is_none()
            && parsed.host_str().is_some()
            && parsed.path() == "/"
            && parsed.query().is_none()
            && parsed.fragment().is_none(),
        "MAILFLOW_DESKTOP_ORIGIN must be a plain HTTPS origin"
    );
    parsed.origin().ascii_serialization()
}
