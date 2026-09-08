# macOS desktop shell

Mailflow for macOS is a Tauri 2 host for the same SPA served by a Mailflow installation. The web origin remains responsible for HTML, authentication cookies, REST, and WebSocket traffic. The desktop process does not proxy mail data, persist credentials, or grant native IPC access to remote content.

## Origins and builds

Development compiles against `http://127.0.0.1:4310`. A different development origin can be selected at compile time with `MAILFLOW_DESKTOP_DEV_ORIGIN`; plain HTTP is accepted only for `localhost`, `127.0.0.1`, or `::1`.

Every production build must embed exactly one installation origin:

```bash
MAILFLOW_DESKTOP_ORIGIN=https://mail.example.com bun run --cwd apps/desktop build
```

The value must be a bare HTTPS origin. Credentials, paths, queries, fragments, and non-HTTPS production URLs are rejected before the main window is created. A build is therefore bound to one self-hosted installation and cannot silently switch to another server. Keychain-backed native sessions are tracked separately and are not part of this shell.

## Navigation boundary

The main window permits navigation only when scheme, host, and effective port match the embedded installation origin. All `http`, `https`, and `mailto` destinations outside that origin open in the default system application. Other schemes are denied. Requests for a new webview are always denied, so OAuth providers, documentation, and message links cannot become privileged child webviews.

The pre-release macOS bundle identifier is `dev.tutitoos.mailflow`. Treat it as a stable application identity when adding Keychain access groups, signing, notarization, and updater signatures in later phases.

The capability assigned to `main` is local-only and contains no native command permissions. Because the production window is remote, its scripts receive no Tauri IPC authority. Deep-link, external-open, single-instance, and window-state behavior executes in Rust.

## OAuth and deep links

The SPA marks desktop OAuth transactions in the existing single-use server state. Google and Microsoft still receive the installation's HTTPS callback. After consuming the state, the API returns one of these fixed operating-system callbacks:

- `mailflow://open/settings/accounts?google=connected`
- `mailflow://open/settings/accounts?microsoft=connected`
- the same success callbacks with `sync=pending`
- a fixed `google=failed` or `microsoft=failed` result

The app accepts only `/`, `/settings/accounts`, `/admin`, and known Admin sections. Account-result query keys and values are allowlisted; search text, message identifiers, arbitrary return URLs, fragments, credentials, and private content are rejected. A valid callback focuses the existing single instance and navigates it back to the installation origin.

On macOS, deep links work only for a bundled and installed application because the scheme is registered in its `Info.plist`. Use a sanitized test account and an installed `.app` when validating the complete OAuth callback.

## Lifecycle and failure behavior

Only one desktop process remains active. A second launch focuses the main window, and a deep-link launch is delivered to that instance. Window size and position are restored by the native window-state plugin. Normal Tauri shutdown saves that state; no mail content or protocol URL is written by Mailflow desktop.

If the configured installation is unavailable, the operating-system webview shows its connection failure and no alternate origin is attempted. Offline mail is intentionally deferred to the encrypted SQLite cache work.

## Validation

Run the pure origin, navigation, and deep-link tests plus the repository checks:

```bash
cargo test --manifest-path apps/desktop/src-tauri/Cargo.toml
cargo check --locked --manifest-path apps/desktop/src-tauri/Cargo.toml
make check
```

Before a release, install the produced `.app` and verify launch, every current SPA route, window restoration, same-origin navigation, external links, both OAuth success/failure callbacks, server-unavailable behavior, and clean shutdown. Signing, notarization, DMG production, and updater validation remain separate roadmap work.

## Resumen en español

La aplicación de macOS carga la misma SPA desde el origen HTTPS fijado durante la compilación. El contenido remoto no recibe permisos IPC; Rust limita la navegación al origen configurado, abre OAuth y enlaces externos fuera del webview, valida callbacks `mailflow://` cerrados, conserva la ventana y mantiene una sola instancia. La caché offline, Keychain, firma, notarización y updater se implementan en Issues posteriores.
