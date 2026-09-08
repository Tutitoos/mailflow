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

The defaults can be changed with `MAILFLOW_IMAP_ACCOUNT_REFRESH`, `MAILFLOW_IMAP_IDLE_HEARTBEAT`, `MAILFLOW_IMAP_POLL_INTERVAL`, `MAILFLOW_IMAP_RECONNECT_MIN`, `MAILFLOW_IMAP_RECONNECT_MAX`, `MAILFLOW_IMAP_LEASE_RENEW`, `MAILFLOW_IMAP_BURST_WINDOW`, and `MAILFLOW_IMAP_MAX_CONNECTIONS`. Duration values use Go duration syntax. The watcher only detects work; the durable synchronization run advances message checkpoints after its database transaction commits.

## Message synchronization and threading

- The worker snapshots every selectable folder's `UIDVALIDITY` and `UIDNEXT`, backfills the recent 90-day window first, and then continues through older mail with bounded UID pages.
- Incremental pages resume from the saved next UID. A changed `UIDVALIDITY` invalidates only that account checkpoint and starts the existing reconciliation recovery instead of guessing which old UID maps to which message.
- The daily full reconciliation records a bounded UID snapshot for each selectable folder. Locations missing from that snapshot are unlinked, and a message is soft-deleted only when it has no remaining folder location, so remote expunges do not leave stale mail or erase valid copies.
- MIME is fetched with `BODY.PEEK[]`, bounded before parsing, normalized into sanitized HTML and plain text, and stored without protocol transcripts.
- A stable message ID is derived from a valid RFC `Message-ID`; messages without one use a SHA-256 digest of the bounded raw message. Malformed references are ignored.
- Thread identity uses the first valid `References` value, then `In-Reply-To`, then the message's own ID. PostgreSQL uniqueness remains account-scoped, so identical RFC IDs in two accounts never create a shared conversation.
- IMAP location is a separate record containing mailbox identity, `UIDVALIDITY`, and UID. When MOVE or COPY assigns a new UID, Mailflow updates that record and retains one normalized domain message.

## Actions, drafts, attachments, and SMTP

- Read, unread, starred, and important actions use silent UID STORE mutations. Thread actions expand to the stable messages in that owner-scoped thread before reaching IMAP.
- Archive, trash, and restore use UID MOVE when advertised. The fallback uses COPY, silent `\\Deleted`, and UID EXPUNGE only when UIDPLUS can identify exactly the affected message; Mailflow never substitutes a mailbox-wide EXPUNGE.
- Draft checkpoints append a complete RFC message to the discovered Drafts folder. UIDPLUS is required so the returned draft locator can be updated or discarded without searching private content.
- SMTP receives exactly one attempt from the provider. Loss of the connection during or after `DATA` is an ambiguous outcome, which the durable outbound-delivery record exposes without retrying and risking a duplicate. A confirmed SMTP delivery remains successful even if the best-effort Sent-folder append fails.
- Bcc recipients remain in the SMTP envelope while the `Bcc` header and its folded continuation lines are removed from the delivered payload.
- Attachment metadata uses an opaque MIME part index. Download re-fetches the message from its current owner-scoped location and applies the same raw, part, and MIME limits before the CDN cache receives bytes.

The protocol implementation uses the maintained `go-imap/v2` client for response framing and literals while Mailflow retains its own domain cursors, persistence rules, and safety policy. Sanitized protocol fixtures, MIME corpora, UID move tests, SMTP ambiguity tests, and PostgreSQL owner-isolation tests run in CI. A protected iCloud or generic-server check remains manual because repository and CI environments must not contain real credentials or private mail.
