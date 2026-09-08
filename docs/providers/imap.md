# IMAP and SMTP setup

Mailflow can connect a generic mail account with IMAP for reading and SMTP for sending. This first increment verifies and stores the account configuration; folder synchronization is delivered separately.

## Security policy

- Use implicit TLS or STARTTLS for both protocols. Cleartext connections are rejected.
- Mailflow always verifies the server certificate and hostname. There is no certificate-bypass option.
- Credentials are encrypted with the installation master key before PostgreSQL persistence and are never returned by the API.
- A connection test opens temporary sessions, verifies both protocols, discovers a bounded capability allowlist, and closes the sessions immediately.
- Disconnecting disables the account and atomically replaces its encrypted credential payload. Reconnect the account by entering fresh credentials.

## Common settings

For iCloud Mail, use your full Apple Account email address and an app-specific password. The usual server settings are `imap.mail.me.com:993` with implicit TLS and `smtp.mail.me.com:587` with STARTTLS. Confirm current values in Apple's documentation before configuring an installation.

For another provider, obtain the exact IMAP and SMTP hostnames, ports, TLS modes, username, and app-specific password from that provider. OAuth-based IMAP, automatic discovery, Exchange protocols, and insecure servers are not supported.

## Failure recovery

Mailflow reports certificate identity, timeout, authentication, capability, and protocol failures separately without including credentials or server transcripts. Correct the indicated setting and test again. If a provider rotates a password, disconnect and add the account again with the replacement credential.

Protected real-account checks must use a dedicated test mailbox and must never record credentials, addresses, subjects, bodies, or screenshots in the repository or CI output.
