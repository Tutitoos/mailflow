# Operational alerts

Mailflow sends privacy-safe operational alerts through an independently
configured SMTP account. Configure `MAILFLOW_ALERT_SMTP_HOST`, port, username,
sender and destination in `deploy/.env`; put the password only in
`deploy/secrets/alert_smtp_password`. Port 587 requires STARTTLS. Set
`MAILFLOW_ALERT_SMTP_IMPLICIT_TLS=true` for an implicit-TLS endpoint such as
port 465.

The policies cover provider authentication, synchronization backlog, low disk
capacity, failed or overdue backups, unavailable Sentry ingestion and service
health. Redis heartbeats let the worker detect unavailable API and Sentry
ingestion components. Policies accept only fixed names plus bounded operational tokens.
Mail addresses, account IDs, subjects, recipients, message bodies, credentials,
tokens, cookies, signed URLs and arbitrary error strings are never persisted or
published as alert data.

An incident is keyed by a SHA-256 digest of its policy and operational key.
Repeated observations update `lastSeenAt` but send at most one message per
30-minute cooldown. A later reminder retains the same incident ID, and recovery
creates a correlated delivery. Incidents and deliveries are retained for 90
days and queried through bounded Admin responses.

## Fallback and testing

Connected-account fallback is disabled by default. Enabling
`MAILFLOW_ALERT_CONNECTED_ACCOUNT_FALLBACK` only permits an installed fallback
transport; it never silently turns a connected mailbox into an operational
sender. The fallback contract receives the failing provider as an exclusion,
so a provider-authentication incident cannot be delivered through that same
provider.

Admin shows recent incidents and delivery outcomes at `GET
/api/v1/admin/alerts`. The owner can send an idempotent template-only test via
the Alerts screen. A failed test exposes an allowlisted error code, not the SMTP
response or configuration value.

If delivery fails, verify DNS, TLS, sender authorization and the secret file
outside Mailflow. Do not paste SMTP transcripts into issues because providers
may include addresses or server identifiers in their replies.
