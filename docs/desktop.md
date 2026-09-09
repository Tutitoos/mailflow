# macOS desktop shell

Mailflow for macOS is a Tauri 2 host for the same SPA served by a Mailflow installation. The web origin remains responsible for HTML, authentication cookies, REST, and WebSocket traffic. The desktop process does not proxy provider credentials. Its remote IPC surface is limited to the encrypted cache, native session, notification, badge, and native-command APIs described here and in [`offline-cache.md`](offline-cache.md) and [`native-sessions.md`](native-sessions.md).

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

The capability assigned to `main` exposes only application commands for account-scoped offline-cache status, reads, writes, and erasure. It exposes no file path, SQL, shell, generic HTTP, opener, Keychain, or window command. Tauri first applies its remote capability and every Rust command independently checks that the top-level webview still matches the exact configured origin. Deep-link, external-open, single-instance, and window-state behavior remains entirely in Rust.

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

After one successful desktop load, a desktop-only service worker keeps the versioned SPA shell available without caching API or authentication responses. If the installation becomes unavailable, the shell reads only authenticated local ciphertext that is still within its retention policy. It never attempts an alternate origin. A first launch cannot work offline because no shell or account cache exists yet.

## Notifications, badge, menus, and shortcuts

Desktop notifications are opt-in. The first enable action uses the macOS notification permission sheet and persists only the enabled flag, preview preference, and bounded hashes used to suppress duplicate events. The default preview is generic and contains neither sender nor subject. The owner can explicitly choose sender-only or full previews from Settings. Denied permission leaves notifications disabled and is shown without repeatedly prompting.

The SPA derives notification candidates from newly observed unread threads after establishing an inbox baseline. Rust validates every event, account, and thread identifier, bounds and strips control characters from optional previews, and suppresses the last 256 event hashes across restarts. No body, recipient, credential, token, or preview text is written to the native settings file. The Dock badge is the bounded total inbox unread count for connected accounts.

Selecting a notification focuses the existing app and queues only its validated account and thread identifiers. The authenticated SPA consumes that target after its authorization gate, verifies that the account still belongs to the owner, and opens the exact conversation. The target is kept in process memory only and is cleared after consumption; it never appears in the URL or on disk.

The native **Mailbox** menu exposes these standard commands:

- New Message: <kbd>Command</kbd>+<kbd>N</kbd>
- Search Mail: <kbd>Command</kbd>+<kbd>K</kbd>
- Inbox: <kbd>Command</kbd>+<kbd>1</kbd>
- Refresh: <kbd>Command</kbd>+<kbd>R</kbd>
- Settings: <kbd>Command</kbd>+<kbd>,</kbd>

Menu labels remain available to VoiceOver and dispatch a closed command allowlist to the authenticated SPA. These integrations add no native animation; the SPA continues to honor `prefers-reduced-motion`.

Signed application updates use a separately configured Tauri channel with explicit check, confirmation, download, cancellation, installation, and restart states. See [`desktop-updater.md`](desktop-updater.md) for the manifest, trust, rollback, and key-rotation contract. Server updates remain non-privileged and separate.

## Validation

Run the pure origin, navigation, and deep-link tests plus the repository checks:

```bash
cargo test --manifest-path apps/desktop/src-tauri/Cargo.toml
cargo check --locked --manifest-path apps/desktop/src-tauri/Cargo.toml
make check
```

Before a release, install the produced `.app` and verify launch, every current SPA route, window restoration, same-origin navigation, external links, both OAuth success/failure callbacks, server-unavailable behavior, and clean shutdown. Also verify the permission sheet on a fresh macOS notification state, all three privacy modes with sanitized fixtures, duplicate suppression, notification click routing after sign-in, Dock badge clearing, each menu shortcut, and VoiceOver menu announcements. Signing, notarization, DMG production, and updater validation remain separate roadmap work.

## Resumen en español

La aplicación de macOS carga la misma SPA desde el origen HTTPS fijado durante la compilación. Rust limita la navegación, abre OAuth y enlaces externos fuera del webview, valida callbacks `mailflow://` cerrados, conserva la ventana y mantiene una sola instancia. El IPC remoto está cerrado a la caché cifrada, la sesión nativa y la experiencia nativa documentada. Las notificaciones requieren permiso, ocultan el contenido por defecto, evitan duplicados y solo abren objetivos validados después de autenticar. El menú nativo ofrece redacción, búsqueda, bandeja, actualización y ajustes con atajos estándar. El updater añade firma minisign, canal y compatibilidad cerrados, confirmación manual, cancelación y reinicio explícito; la firma y notarización de Apple se implementan por separado.
