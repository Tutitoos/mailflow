# Sentry-compatible ingestion

Mailflow accepts the official SDK envelope protocol at `/sentry/api/1/envelope/` and legacy JSON events at `/sentry/api/1/store/`. Four component projects (`api`, `web`, `desktop`, and `ios`) receive stable public keys derived with HMAC from the installation master key. The key selects a component; it does not grant access to Admin data.

The foundation accepts events, exceptions, breadcrumbs, sessions, transactions and spans, attachments, client reports, and check-ins. Error events are grouped into issues using a deterministic, component-and-environment-scoped fingerprint. Profiles and Replay remain separate roadmap work.

## Privacy and bounds

- Envelopes are limited to 5 MiB, 100 items, 120 requests per project per minute, and 1 GiB of accounted local storage by default.
- PostgreSQL stores allowlisted metadata, counts, byte sizes, and SHA-256 hashes. It never stores messages, exception values, breadcrumb text, mail fields, headers, credentials, or raw identifiers.
- Arbitrary attachments are acknowledged but deliberately discarded after hashing. Their contents are never persisted.
- For a JSON item of at least 64 KiB, the Sentry CDN namespace stores only its normalized metadata summary, never the original payload.
- Events expire after 30 days. Daily cleanup removes associated database rows and CDN summaries.
- Release artifacts expire after the same configured retention period. Source maps are normalized before storage: `sourcesContent` is removed and source paths are reduced to basenames.
- Duplicate component/event IDs are idempotent and return the original protocol ID.

Authentication follows the SDK protocol through `X-Sentry-Auth: Sentry sentry_key=<key>, sentry_version=7` or the browser-compatible `sentry_key` query parameter. Missing or disabled keys return 401; malformed, oversized, rate-limited, and quota-exhausted requests return 400, 413, 429, and 507 respectively.

The ingestion routes are registered before the Fiber Sentry adapter, preventing failed ingestion from capturing itself and creating a feedback loop.

## Issues and release artifacts

Admin can list issues at `GET /api/v1/admin/sentry` and move an issue between `unresolved`, `resolved`, and `ignored` with `PUT /api/v1/admin/sentry/{issueId}`. Exception values, messages, mail data, and raw fingerprints are excluded; only allowlisted exception types and normalized frames participate in grouping.

Minimal `sentry-cli`-compatible routes are available for the fixed `mailflow` organization:

- `POST /api/0/organizations/mailflow/releases/` creates a release.
- `POST /api/0/projects/mailflow/{component}/releases/{version}/files/` uploads a source map, dSYM, or DIF.

Each component receives a separate 256-bit artifact token derived from the installation master key. Only its SHA-256 digest is persisted; the public DSN key cannot upload build artifacts, and a token cannot cross component boundaries. An installation owner can explicitly retrieve one inside the trusted API container with `docker compose exec api sentry-token web` (replace `web` with the target component), then pass it to `sentry-cli` as its auth token. The value must remain in secret storage and must never be logged. Artifacts are limited to 20 MiB each and 2 GiB total by default. The worker validates and indexes uploads asynchronously with five retry attempts. Source maps resolve original filenames, positions, and safe function names; native artifacts resolve instruction addresses using Mach-O or ELF symbol tables, including supported dSYM ZIPs. Original normalized event metadata remains intact when processing fails.
