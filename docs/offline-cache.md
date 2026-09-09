# Encrypted macOS offline cache

Mailflow desktop keeps a read-only, account-scoped subset of server responses in SQLite so an initialized installation remains useful during a network interruption. Providers and PostgreSQL remain authoritative. The cache never becomes a queue and never writes remote actions, drafts, attachments, credentials, session tokens, or provider cursors back to the server.

## Stored data and policy

The desktop caches the public account descriptors returned to the mail shell, mailbox and label navigation, inbox pages, previously requested search result pages, and conversations that the user opened. Cursor-specific pages use independent opaque cache keys, so pagination checkpoints cannot overwrite each other. An exact search can be reopened offline after it has succeeded online; Mailflow does not claim to provide arbitrary full-history offline search.

Every record expires 90 days after its latest successful refresh. Expired rows are removed on normal reads and writes. Each account is limited to 10,000 non-profile records, evicted oldest-first, and each serialized response is limited to 2 MiB. These limits bound disk use and IPC payloads while allowing substantially more than the default working set for a personal installation.

The desktop service worker caches only `/`, the known SPA routes, and their fingerprinted assets. It excludes `/api/`, authentication, mail responses, unknown navigation paths, and every non-GET request. Navigation uses network-first refresh with the cached shell as an offline fallback. Each install removes assets that are no longer referenced. The first desktop launch therefore requires the installation to be reachable.

## Encryption and local identity

SQLite contains SHA-256 account and lookup hashes plus AES-256-GCM nonces and ciphertext. Account IDs, search expressions, subjects, previews, addresses, bodies, and serialized response JSON are not stored in plaintext. Associated data binds every ciphertext to its account hash, record kind, and cache-key hash, preventing records from being moved between scopes.

Each account receives an independent random 256-bit key stored as a generic-password item in the macOS Keychain service `dev.tutitoos.mailflow.offline-cache`. Keys never cross IPC and never enter SQLite. Removing an account destroys its Keychain key before deleting and vacuuming its rows; even if the filesystem step fails, any remaining ciphertext is no longer decryptable. Native authentication uses the separate `dev.tutitoos.mailflow.native-session` service documented in [Native sessions](native-sessions.md). Logout and recovery destroy the native session and all account cache keys before the signed-out shell can continue.

The database is created under Tauri's application-local-data directory with mode `0600`, WAL, full synchronization, foreign-key checks, and secure deletion enabled. Schema migrations run transactionally through SQLite `user_version`. A schema newer than the application is rejected instead of downgraded.

## Native boundary

The webview can call only these application operations:

- check whether a usable cached account exists;
- list decrypted cached account descriptors;
- store a complete successful account list;
- read or write one allowlisted `navigation`, `inbox`, `search`, or `conversation` record;
- remove one account and its key.
- remove the native session and all cached account material during logout or recovery.

No command accepts SQL, a filesystem path, a URL, raw key material, or an unbounded record type. At build time, Tauri's remote capability is generated for only the exact installation origin and the `main` window. Rust independently compares the current top-level scheme, host, and effective port with that embedded origin before every operation. All navigation outside that origin is denied by the shell.

## Failure and reconciliation

SQLite runs `quick_check` once when the desktop process first opens the cache. Confirmed `SQLITE_CORRUPT` or `SQLITE_NOTADB`, or a failed integrity result, deletes the database and WAL sidecars and creates a clean schema. An individual record with an invalid authentication tag or invalid JSON is deleted and treated as a cache miss. Neither path sends a remote mutation.

Offline mode is read-only. Actions, composing, attachment fetches, and draft persistence continue to fail closed until the installation returns. When the browser reports connectivity again, the existing REST and WebSocket flows refresh the current account, navigation, inbox, and conversation state from the authoritative server. Because no offline action or draft queue exists, reconciliation cannot duplicate either.

## Validation

Run:

```bash
cargo test --locked --manifest-path apps/desktop/src-tauri/Cargo.toml
cargo clippy --locked --manifest-path apps/desktop/src-tauri/Cargo.toml --all-targets -- -D warnings
bun run --cwd apps/web test
make check
```

Rust tests verify encryption at rest, account partitioning, key destruction, expiry, corrupt-database rebuild, and authenticated-record rejection. Web tests verify that browsers never invoke the native bridge and that desktop requests write successful pages, reuse the same cursor offline, and erase cache state after disconnect.

## Resumen en español

La app de macOS conserva durante 90 días una copia de solo lectura de cuentas, navegación, bandejas, búsquedas ya realizadas y conversaciones abiertas. SQLite solo contiene hashes y datos cifrados con AES-256-GCM; cada cuenta usa una clave aleatoria independiente guardada en Keychain. Al eliminar la cuenta se destruye primero su clave. La corrupción reconstruye una caché vacía sin modificar el servidor, y al volver la conexión PostgreSQL y el proveedor vuelven a ser la única autoridad.
