# macOS release pipeline

Mailflow's `Desktop release` workflow is the only supported path for direct
macOS distribution. It starts from an existing `vX.Y.Z` stable tag or
`vX.Y.Z-beta.N` beta tag, builds a single universal application, signs it with
a **Developer ID Application** identity, submits it to Apple's notary service,
staples the returned tickets, and creates or updates a draft GitHub release.
It never publishes the draft automatically.

The workflow runs on the protected `desktop-release` GitHub Environment. Its
deployment policy accepts only `v*` tags; the workflow then enforces the two
documented version forms before it reads signing configuration. Installations
that add environment reviewers must keep self-review policy compatible with
their release operator.

Pull requests that change desktop or release inputs run a separate secretless
contract job. It cross-compiles both Rust targets, creates an ad-hoc-signed
universal `.app`, checks both architecture slices and the signature structure,
and rejects debug entitlements. This is compilation evidence only; it cannot
substitute for Developer ID signing or Apple's notarization response.

## Protected configuration

Repository or environment variables contain public build metadata:

- `MAILFLOW_DESKTOP_ORIGIN`: the credential-free HTTPS Mailflow installation origin.
- `MAILFLOW_DESKTOP_UPDATE_PUBLIC_KEY`: the base64 Tauri minisign public key.
- `MAILFLOW_DESKTOP_UPDATE_ENDPOINT_STABLE`: the stable manifest endpoint.
- `MAILFLOW_DESKTOP_UPDATE_ENDPOINT_BETA`: the beta manifest endpoint.
- `APPLE_TEAM_ID`: the ten-character Apple Developer team identifier.

Environment secrets contain private signing material:

- `APPLE_CERTIFICATE`: base64 of a password-protected `.p12` containing exactly
  one Developer ID Application certificate and private key.
- `APPLE_CERTIFICATE_PASSWORD` and `KEYCHAIN_PASSWORD`.
- `APPLE_API_ISSUER`, `APPLE_API_KEY`, and `APPLE_API_PRIVATE_KEY`; the last is
  base64 of the App Store Connect `.p8` key used by `notarytool` through Tauri.
- `TAURI_SIGNING_PRIVATE_KEY` and `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` for updater artifacts.

Do not use an Apple Distribution, Apple Development, ad-hoc, or Mac App Store
certificate. The workflow extracts the identity from an ephemeral keychain and
fails unless it finds exactly one Developer ID Application identity. It also
fails on missing values, a tag/version mismatch, an invalid channel, or an
attempt to overwrite an already-published release. Ephemeral certificate and
API-key files are destroyed in the final cleanup step.

Losing the updater private key prevents installed copies from trusting future
updates. Back it up separately from Apple credentials. Rotate it only through
a release trusted by the previous key, as described in
[`desktop-updater.md`](desktop-updater.md).

## Artifacts and release state

The workflow builds both `aarch64-apple-darwin` and `x86_64-apple-darwin`
through Tauri's `universal-apple-darwin` target. A release configuration overlay
enables signed updater artifacts without placing a fake or
installation-specific trust anchor in the repository.

The draft release and the 30-day workflow artifact contain:

- `Mailflow_<version>_universal.dmg`
- `Mailflow_<version>_universal.app.tar.gz`
- `Mailflow_<version>_universal.app.tar.gz.sig`
- `mailflow-stable.json` or `mailflow-beta.json`
- `Mailflow_<version>_universal.cdx.json`
- `native-release-evidence.json`
- one `.sigstore.json` bundle for both the DMG and updater archive
- downloadable GitHub provenance and SBOM attestation bundles
- `macos-SHA256SUMS`

The updater manifest is generated from a fixed schema, a credential-free HTTPS
asset URL, the exact signature, the tag commit time, and bounded release notes.
The workflow verifies both keyless artifact signatures, attaches GitHub build
provenance and associates the CycloneDX SBOM with both installable artifacts. A
rerun may replace assets only while the release remains a draft. The complete
independent verification sequence is documented in
[`supply-chain.md`](supply-chain.md).

## Verification gate

Before any upload, `scripts/verify-macos-release.sh` proves all of the following
for the built app, mounted DMG app, updater-archive app, and a clean temporary
installation:

- bundle identifier and version are exact;
- the executable contains arm64 and x86_64 slices;
- deep strict code-signing validation succeeds with hardened runtime and the expected team;
- `com.apple.security.get-task-allow` is not enabled;
- Gatekeeper accepts the app and DMG;
- Apple notarization tickets are stapled and valid;
- the DMG verifies and mounts read-only;
- no private local rpath is present;
- the clean copy launches, remains running, shuts down, and can be removed.

An integration test verifies the updater archive using the same minisign
library as Tauri and then changes one byte to prove the tampered artifact is
rejected. Release-evidence tests separately alter artifact and SBOM bytes.
`macos-SHA256SUMS` is generated only after every gate and keyless signature passes.

Publishing the draft remains a separate human release decision. Before
publishing, download the workflow artifact on a second supported Mac, compare
checksums, install from the DMG, authenticate against a non-production fixture,
exercise an update from the previous signed version, and uninstall the test
copy. Record only checksums, tag, commit, notarization request identity, and
sanitized results.

## Local checks

Without protected Apple material, contributors can still run:

```bash
bun test scripts/desktop-release-manifest.test.ts
bash -n scripts/verify-macos-release.sh
cargo test --manifest-path apps/desktop/src-tauri/Cargo.toml --locked
```

A real notarization cannot be simulated with an Apple Distribution or ad-hoc
certificate. Local dry runs may validate universal compilation and layout, but
only the protected workflow and Apple's accepted ticket satisfy the release
gate.

## Resumen en español

El workflow protegido crea una app universal y un DMG para distribución
directa, firmados con Developer ID Application, notarizados y grapados. Valida
arquitecturas, firma, Gatekeeper, tickets, montaje, instalación limpia,
arranque, updater y rechazo de alteraciones antes de subir artefactos. La
release siempre queda en borrador: publicarla exige una decisión humana y una
prueba final en otro Mac. Ninguna clave privada se guarda en el repositorio ni
en los artefactos.
