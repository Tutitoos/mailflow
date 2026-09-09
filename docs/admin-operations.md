# Admin operations

The Admin area is an owner-authenticated operational surface at `/admin`. Its
overview reads bounded status from `GET /api/v1/admin/status` and classifies each
component as `healthy`, `degraded`, `blocked`, or `stale`. PostgreSQL and Redis
are probed directly, queue pressure is summarized without payloads, and the
worker writes a short-lived Redis heartbeat every ten seconds. A heartbeat older
than 45 seconds is stale; the key expires after two minutes.

The section routes expose existing first-party metrics, redacted logs, grouped
Sentry issues, EN/ES translation revisions, aggregate CDN usage, and bounded
backup scheduler and operational-alert history. The Alerts section exposes only
allowlisted incident codes and delivery outcomes; its test action uses a
fixed template and an idempotency key. `GET /api/v1/admin/backups` is read-only; execution
and restore remain operator commands documented in [Encrypted backups and
restore](backups.md). Server updates remain informational. The installed macOS
app exposes its separately signed, user-confirmed [desktop updater](desktop-updater.md)
in the same screen without granting the server any update capability. Admin never
receives Docker socket access, arbitrary SQL, or a privileged server update command.

## Queue controls and audit

`POST /api/v1/admin/queue/retry` requires the authenticated owner, an account ID,
the literal confirmation `retry`, and an `Idempotency-Key` between 16 and 128
characters. The sync scheduler performs the account ownership check. Reusing a
key returns the original operation and does not enqueue another reconciliation.

The `admin_operations` audit table stores only the owner, fixed action and result,
timestamps, and SHA-256 digests of the account target and idempotency key. The API
never returns either digest. `GET /api/v1/admin/queue` returns queue counts and at
most 25 recent operations for the current owner.

## Privacy and failure handling

Admin responses contain operational metadata only. They must never include mail
bodies, subjects, recipients, provider payloads, credentials, tokens, cookies,
signed URLs, object paths, or raw account identifiers. Metrics and log retention
remain defined in [Internal metrics](metrics.md) and [Operational logs](logs.md);
Sentry retention remains defined in [Sentry-compatible ingestion](sentry-ingestion.md).
Alert delivery, cooldowns and its 90-day incident retention are defined in
[Operational alerts](alerts.md).

If PostgreSQL or Redis is unavailable, status reports the affected component as
blocked and mutation endpoints return RFC 9457 Problem Details. A missing worker
heartbeat reports stale rather than healthy. Operators should restore the failed
service and refresh Admin; retrying a mail sync is safe only through the explicit,
idempotent confirmation flow.
