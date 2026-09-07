# Operational logs

Mailflow writes every accepted `slog` record as redacted JSON to stdout, which remains the container source of truth. When PostgreSQL is available, the same sanitized record enters a non-blocking queue capped at 2,000 entries and is committed in batches of at most 100. A database failure retains one bounded batch for retry; new pressure is counted and dropped instead of blocking mail or synchronization work.

## Privacy boundary

The pipeline redacts prohibited keys recursively, including credentials, tokens, cookies, signed URLs, account/message identifiers, subjects, bodies, senders, recipients, and email fields. It also rejects email-like strings, bearer values, JWTs, oversized values, unknown structured types, and absolute URLs containing user information or query parameters. Redaction happens before stdout, persistence, and streaming.

Stored attributes are capped at 16 KiB. Stable `service`, `module`, `level`, `event`, and optional `requestId` fields are indexed and validated separately. Operational code must never use a message body or another personal value as a log message, event name, filter dimension, or request identifier.

## Retention, queries, and live events

Info, warning, and error records are retained for 30 days. `GET /api/v1/admin/logs` is owner-authenticated and accepts bounded filters for time, service, module, level, event, and `requestId`. Successfully committed records publish a metadata-only `admin.log` event through the existing authenticated, resumable WebSocket; attributes and messages are not included in that event.

Debug is disabled by default. Admin can activate it for at most one hour through `PUT /api/v1/admin/logs/debug`; a duration of zero revokes the lease immediately. API and worker refresh the persisted lease, so it expires automatically even after a restart.

Migration `00018_structured_logs.sql` adds the debug lease and query index. Its rollback removes debug records before restoring the original level constraint, while preserving info, warning, and error history.
