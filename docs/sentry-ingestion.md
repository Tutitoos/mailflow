# Sentry-compatible ingestion

Mailflow accepts the official SDK envelope protocol at `/sentry/api/1/envelope/` and legacy JSON events at `/sentry/api/1/store/`. Four component projects (`api`, `web`, `desktop`, and `ios`) receive stable public keys derived with HMAC from the installation master key. The key selects a component; it does not grant access to Admin data.

The foundation accepts events, exceptions, breadcrumbs, sessions, transactions and spans, attachments, client reports, and check-ins. Grouping, symbolication, profiles, and Replay remain separate roadmap work.

## Privacy and bounds

- Envelopes are limited to 5 MiB, 100 items, 120 requests per project per minute, and 1 GiB of accounted local storage by default.
- PostgreSQL stores allowlisted metadata, counts, byte sizes, and SHA-256 hashes. It never stores messages, exception values, breadcrumb text, mail fields, headers, credentials, or raw identifiers.
- Arbitrary attachments are acknowledged but deliberately discarded after hashing. Their contents are never persisted.
- For a JSON item of at least 64 KiB, the Sentry CDN namespace stores only its normalized metadata summary, never the original payload.
- Events expire after 30 days. Daily cleanup removes associated database rows and CDN summaries.
- Duplicate component/event IDs are idempotent and return the original protocol ID.

Authentication follows the SDK protocol through `X-Sentry-Auth: Sentry sentry_key=<key>, sentry_version=7` or the browser-compatible `sentry_key` query parameter. Missing or disabled keys return 401; malformed, oversized, rate-limited, and quota-exhausted requests return 400, 413, 429, and 507 respectively.

The ingestion routes are registered before the Fiber Sentry adapter, preventing failed ingestion from capturing itself and creating a feedback loop.
