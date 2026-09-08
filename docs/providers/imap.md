# IMAP and SMTP setup

Mailflow can connect a generic mail account with IMAP for reading and SMTP for sending. It verifies the account configuration and discovers the server folder hierarchy before message synchronization begins.

## Security policy

- Use implicit TLS or STARTTLS for both protocols. Cleartext connections are rejected.
- Mailflow always verifies the server certificate and hostname. There is no certificate-bypass option.
- Credentials are encrypted with the installation master key before PostgreSQL persistence and are never returned by the API.
- A connection test opens temporary sessions, verifies both protocols, discovers a bounded capability allowlist, and closes the sessions immediately.
- Folder discovery requests `NAMESPACE`, `LIST`, `LSUB`, and bounded `STATUS` metadata over an authenticated TLS session. Passwords and server transcripts are never stored in folder records or logs.
- Disconnecting disables the account and atomically replaces its encrypted credential payload. Reconnect the account by entering fresh credentials.

## Common settings

For iCloud Mail, use your full Apple Account email address and an app-specific password. The usual server settings are `imap.mail.me.com:993` with implicit TLS and `smtp.mail.me.com:587` with STARTTLS. Confirm current values in Apple's documentation before configuring an installation.

For another provider, obtain the exact IMAP and SMTP hostnames, ports, TLS modes, username, and app-specific password from that provider. OAuth-based IMAP, automatic server discovery, Exchange protocols, and insecure servers are not supported.

## Folder identity and cursors

- Mailflow maps advertised `SPECIAL-USE` folders to Inbox, Sent, Drafts, Trash, Junk, Archive, and All Mail. Other folders keep their provider name and hierarchy.
- Nonselectable hierarchy nodes are visible metadata but never receive a message cursor or synchronization job.
- Subscription state is advisory. Selectable folders are discovered even when the server omits them from `LSUB`.
- Custom-folder identity removes the personal namespace and normalizes the hierarchy delimiter, so the same path is not duplicated when a server changes `/` to `.` or reports another namespace delimiter.
- A uniquely matched custom-folder rename keeps the existing Mailflow mailbox and cursor. If multiple prior folders could match, reconciliation stops with an identity conflict instead of choosing silently.
- Each selectable folder owns its own `UIDVALIDITY`, provider `UIDNEXT`, and next local UID. When `UIDVALIDITY` changes, only that folder resets to UID 1 and enters `resync_required`; other folder cursors remain active.
- Missing folders are retained as nonselectable `missing` records. They are not synchronized and can be reconciled safely if the provider reports them again.

## Failure recovery

Mailflow reports certificate identity, timeout, authentication, capability, protocol, folder-limit, and folder-identity failures separately without including credentials or server transcripts. Re-running discovery is transaction-safe: a failed or ambiguous attempt commits no partial folder changes. Correct the indicated setting and try again. If a provider rotates a password, disconnect and add the account again with the replacement credential.

Protected real-account checks must use a dedicated test mailbox and must never record credentials, addresses, subjects, bodies, or screenshots in the repository or CI output.

## Background change detection

The worker maintains one bounded watcher for each active IMAP account. Servers advertising `IDLE` keep `INBOX` selected and are checked with a 25-minute heartbeat. Unsolicited `EXISTS`, `EXPUNGE`, `FETCH`, or `RECENT` responses end the current IDLE command and trigger a transactional folder-status refresh. Bursts are coalesced for one second so one server update does not create duplicate refreshes.

A dropped session reconnects with exponential backoff from one second to one minute. Detection never advances a folder cursor itself: cursor changes only happen after a successful database reconciliation, so a disconnect cannot skip uncommitted provider data. Servers without `IDLE` close the temporary connection and use the same refresh path every five minutes.

Redis grants a renewable per-account lease before a connection is opened, preventing two workers from watching the same account. The worker defaults to four concurrent IMAP connections and releases both sessions and leases during shutdown. The normal worker heartbeat and the bounded `mailflow_imap_watch_total` metric expose watcher health without account identifiers or message data.

The defaults can be changed with `MAILFLOW_IMAP_ACCOUNT_REFRESH`, `MAILFLOW_IMAP_IDLE_HEARTBEAT`, `MAILFLOW_IMAP_POLL_INTERVAL`, `MAILFLOW_IMAP_RECONNECT_MIN`, `MAILFLOW_IMAP_RECONNECT_MAX`, `MAILFLOW_IMAP_LEASE_RENEW`, `MAILFLOW_IMAP_BURST_WINDOW`, and `MAILFLOW_IMAP_MAX_CONNECTIONS`. Duration values use Go duration syntax. Message retrieval, MIME normalization, threading, and remote actions are deliberately handled by the next synchronization stage rather than the watcher.
