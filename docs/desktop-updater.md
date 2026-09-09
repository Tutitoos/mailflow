# Signed macOS updater

Mailflow uses the official Tauri 2 updater for HTTPS retrieval, minisign verification, platform installation, and its backup-first replacement flow. Mailflow's Rust wrapper adds an explicit release channel, a closed universal-macOS target, manifest compatibility metadata, release-identity confirmation, bounded progress events, cancellation, and sanitized failure codes. The web application never receives the endpoint, signature, or updater public key.

## Build-time trust configuration

The updater is disabled unless both trust inputs are embedded at compile time:

```text
MAILFLOW_DESKTOP_UPDATE_CHANNEL=stable
MAILFLOW_DESKTOP_UPDATE_ENDPOINT=https://releases.example.test/mailflow/stable/latest.json
MAILFLOW_DESKTOP_UPDATE_PUBLIC_KEY=<complete Tauri minisign public key>
```

The channel is `stable` or `beta`; absence means `stable`. The endpoint must be HTTPS and cannot contain credentials or a fragment. A partial or invalid configuration fails closed and is reported in Admin. These values are public build metadata. The corresponding `TAURI_SIGNING_PRIVATE_KEY` and optional password exist only in the protected release environment and must never be committed, logged, placed in Compose, or sent to the app.

Release packaging passes the same public key to a private Tauri configuration
overlay as `plugins.updater.pubkey` and enables `bundle.createUpdaterArtifacts`.
Before any check or download, Mailflow's native updater independently replaces
the plugin value with `MAILFLOW_DESKTOP_UPDATE_PUBLIC_KEY`. The base
configuration contains neither a fake trust anchor nor an update endpoint, and
a build without the complete Mailflow trust configuration keeps update
controls disabled.

The generated overlay has this shape and is never committed:

```json
{
  "bundle": { "createUpdaterArtifacts": true },
  "plugins": { "updater": { "pubkey": "<complete public key>" } }
}
```

Losing the private key prevents trusted updates to existing installations. Rotation therefore requires shipping a release signed by the old key that embeds the next public key before retiring the old secret.

The protected release overlay enables `bundle.createUpdaterArtifacts`, so a signed macOS build produces `Mailflow.app.tar.gz` and `Mailflow.app.tar.gz.sig`. Apple code signing and notarization are an independent trust layer completed by the universal-DMG release work.

## Manifest contract

The endpoint returns `204` when no update exists or a Tauri static manifest with one custom compatibility object:

```json
{
  "version": "1.2.3",
  "notes": "Sanitized release notes",
  "pub_date": "2026-09-09T12:00:00Z",
  "mailflow": {
    "channel": "stable",
    "bundleIdentifier": "dev.tutitoos.mailflow",
    "target": "darwin-universal"
  },
  "platforms": {
    "darwin-universal": {
      "url": "https://releases.example.test/mailflow/v1.2.3/Mailflow.app.tar.gz",
      "signature": "<complete contents of Mailflow.app.tar.gz.sig>"
    }
  }
}
```

Stable builds accept only release SemVer versions. Beta builds accept only `-beta.*` prereleases. Both reject the current version, downgrades, other prerelease families, non-HTTPS artifacts, another bundle identifier, and any target except `darwin-universal`.

Before showing an install prompt, Mailflow hashes the channel, version, download URL, signature, and target into a release identity. It fetches the manifest again after confirmation and requires the identity to be unchanged. The official updater then downloads the artifact and verifies its minisign signature before installation; an altered payload cannot reach replacement.

## User flow and recovery

Admin > Updates shows the current application version, compiled channel, candidate, sanitized notes, progress, and one bounded error message. Checking is explicit. Installation requires a second confirmation and never runs silently. Download can be cancelled; dropping the in-flight verified-download future leaves the installed application untouched. Cancellation, offline checks, malformed manifests, invalid signatures, and changed release identities remain retryable.

After verification, the Tauri installer stages the replacement and first moves the current application into its temporary backup location. An installation error is reported without changing server state. A successful replacement waits in `restart_required` until the owner chooses **Restart Mailflow**. The server update panel stays informational and never receives Docker socket access or an application-update command.

## Validation

Automated tests cover configuration completeness, HTTPS enforcement, channel separation, downgrade rejection, target and bundle compatibility, release-identity changes, bounded notes, cancellation signals, localized error mapping, and progress bounds. Before release, use a disposable signing key and HTTPS fixture channel to exercise valid, tampered, wrong-channel, downgrade, interrupted-download, offline, and manifest-change cases against an installed copy outside the developer checkout. Confirm the existing `.app` remains launchable after every rejected or interrupted case.

Never use a production private key, real mailbox data, account identifiers, private release URLs, or notification screenshots as test evidence.

## Resumen en español

El actualizador de macOS usa el plugin oficial de Tauri para descargar, verificar la firma minisign e instalar. Mailflow añade canal explícito, destino universal, compatibilidad de bundle, identidad inmutable antes de reemplazar, progreso, cancelación y errores redactados. La búsqueda y la instalación siempre son manuales, una descarga interrumpida no modifica la app existente y el reinicio requiere una acción explícita. La clave privada solo vive en el entorno protegido de release; la firma y notarización de Apple se validan por separado.
