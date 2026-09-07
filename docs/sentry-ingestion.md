# Sentry-compatible ingestion

Mailflow accepts the official SDK envelope protocol at `/sentry/api/1/envelope/` and legacy JSON events at `/sentry/api/1/store/`. Four component projects (`api`, `web`, `desktop`, and `ios`) receive stable public keys derived with HMAC from the installation master key. The key selects a component; it does not grant access to Admin data.

The foundation accepts events, exceptions, breadcrumbs, sessions, transactions and spans, profiles, Replay, attachments, client reports, and check-ins. Error events are grouped into issues using a deterministic, component-and-environment-scoped fingerprint.

## Privacy and bounds

- Envelopes are limited to 5 MiB, 100 items, 120 requests per project per minute, and 1 GiB of accounted local storage by default.
- PostgreSQL stores allowlisted metadata, counts, byte sizes, and SHA-256 hashes. It never stores messages, exception values, breadcrumb text, mail fields, headers, credentials, or raw identifiers.
- Arbitrary attachments are acknowledged but deliberately discarded after hashing. Their contents are never persisted.
- For a JSON item of at least 64 KiB, the Sentry CDN namespace stores only its normalized metadata summary, never the original payload.
- Events expire after 30 days. Daily cleanup removes associated database rows and CDN summaries.
- Release artifacts expire after the same configured retention period. Source maps are normalized before storage: `sourcesContent` is removed and source paths are reduced to basenames.
- Traces and profiles expire after 7 days. Only allowlisted IDs, operations, statuses, timestamps, durations, platforms, and aggregate counts are stored; raw profile samples and frames are discarded.
- Replay is disabled by default. When explicitly enabled, every string value is replaced before persistence, malformed chunks are discarded without blocking the rest of an envelope, and stored segments expire after 3 days.
- Replay has a separate 512 MiB storage quota and a 1 MiB decoded limit per segment. The general envelope, item-count, rate, and Sentry storage limits still apply.
- Duplicate component/event IDs are idempotent and return the original protocol ID.

Authentication follows the SDK protocol through `X-Sentry-Auth: Sentry sentry_key=<key>, sentry_version=7` or the browser-compatible `sentry_key` query parameter. Missing or disabled keys return 401; malformed, oversized, rate-limited, and quota-exhausted requests return 400, 413, 429, and 507 respectively.

The ingestion routes are registered before the Fiber Sentry adapter, preventing failed ingestion from capturing itself and creating a feedback loop.

## Tracing, profiles, and Replay

The web client initializes the official Sentry React SDK only when `VITE_SENTRY_DSN` is provided at build time. Trace sampling defaults to 10%. Before sending, the client removes user, request, extra, breadcrumb, exception-message, source-context, span-description, and span-data fields that could contain mail data.

Replay requires both sides of the installation to opt in with `MAILFLOW_SENTRY_REPLAY_ENABLED=true`: Compose passes the value to the API and embeds it into the web build. The SDK then uses 5% session sampling and 10% error sampling. All text and inputs are masked, all media is blocked, mail and composer containers are explicitly blocked, and no selector is unmasked or unblocked. The API repeats masking before writing a segment, so client configuration is not a privacy boundary.

Admin can read bounded 24-hour totals for traces, spans, profiles, Replays, and Replay segments at `GET /api/v1/admin/sentry/telemetry`. The response also states whether Replay persistence is enabled. It never exposes captured payloads.

## Issues and release artifacts

Admin can list issues at `GET /api/v1/admin/sentry` and move an issue between `unresolved`, `resolved`, and `ignored` with `PUT /api/v1/admin/sentry/{issueId}`. Exception values, messages, mail data, and raw fingerprints are excluded; only allowlisted exception types and normalized frames participate in grouping.

Minimal `sentry-cli`-compatible routes are available for the fixed `mailflow` organization:

- `POST /api/0/organizations/mailflow/releases/` creates a release.
- `POST /api/0/projects/mailflow/{component}/releases/{version}/files/` uploads a source map, dSYM, or DIF.

Each component receives a separate 256-bit artifact token derived from the installation master key. Only its SHA-256 digest is persisted; the public DSN key cannot upload build artifacts, and a token cannot cross component boundaries. An installation owner can explicitly retrieve one inside the trusted API container with `docker compose exec api sentry-token web` (replace `web` with the target component), then pass it to `sentry-cli` as its auth token. The value must remain in secret storage and must never be logged. Artifacts are limited to 20 MiB each and 2 GiB total by default. The worker validates and indexes uploads asynchronously with five retry attempts. Source maps resolve original filenames, positions, and safe function names; native artifacts resolve instruction addresses using Mach-O or ELF symbol tables, including supported dSYM ZIPs. Original normalized event metadata remains intact when processing fails.
