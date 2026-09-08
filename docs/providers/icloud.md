# iCloud Mail setup and compatibility

Mailflow connects iCloud Mail through a dedicated setup contract backed by the generic IMAP and SMTP provider. The form asks only for the mailbox display name, the full iCloud Mail address, and an app-specific password. It never labels or requests the primary Apple Account password.

## Before connecting

1. Enable two-factor authentication for the Apple Account.
2. Sign in at [Apple Account](https://account.apple.com/), open **Sign-In and Security**, and generate an app-specific password for Mailflow.
3. In Mailflow, choose **Connect iCloud**, enter the full iCloud Mail address, and paste that app-specific password.
4. Test the connection before saving it, then discover folders to start the initial synchronization.

Changing or resetting the primary Apple Account password revokes existing app-specific passwords. If Apple rejects the credential, generate a new app-specific password instead of entering the primary password. The failure response never includes the address, password, or server configuration.

## Fixed provider preset

The API does not accept caller-controlled server fields on the iCloud endpoints. It always uses the values currently published by Apple:

| Protocol | Host | Port | Transport | Authentication |
| --- | --- | ---: | --- | --- |
| IMAP | `imap.mail.me.com` | 993 | Implicit TLS | Full iCloud Mail address and app-specific password |
| SMTP | `smtp.mail.me.com` | 587 | STARTTLS | Required; same address and app-specific password |

The connected account remains an ordinary IMAP at the domain boundary and gains the bounded `provider.icloud` capability. No iCloud-specific folder override is applied: folder discovery relies on standard IMAP SPECIAL-USE attributes, then the normal UID, MIME, threading, action, draft, attachment, and SMTP contracts run unchanged.

Sources reviewed on 2026-09-08: [Apple iCloud Mail server settings](https://support.apple.com/en-us/102525) and [Apple app-specific password guidance](https://support.apple.com/en-us/102654). Spanish operators can use [Apple server settings in Spanish](https://support.apple.com/es-es/102525); Mailflow remains authoritative only for its own behavior.

## Acceptance matrix

Repository fixtures contain no Apple credentials or private mail. The automated column is the merge gate; the protected column is a release-readiness check run only with a private test account outside CI.

| Capability | Automated contract | Protected iCloud account | Expected result |
| --- | --- | --- | --- |
| Preset and credential wording | Pass | Not required | Only official endpoints and app-specific-password language are exposed |
| TLS and authentication failure | Pass | Required before 1.0 | Certificate verification remains mandatory and rejected credentials show safe remediation |
| Inbox and folder discovery | Pass | Required before 1.0 | SPECIAL-USE folders map without iCloud-only overrides |
| Initial and incremental sync | Pass | Required before 1.0 | MIME, UIDs, flags, and threads use the generic IMAP pipeline |
| Draft create/update/discard | Pass | Required before 1.0 | UIDPLUS produces an exact draft locator |
| Read, star, archive, trash, restore | Pass | Required before 1.0 | UID STORE and MOVE/COPY affect only the selected message locations |
| SMTP and Sent copy | Pass | Required before 1.0 | One SMTP attempt; ambiguous delivery is never retried automatically |
| Attachment retrieval | Pass | Required before 1.0 | Bounded re-fetch returns the requested MIME attachment |

For a protected run, record only the date, Mailflow commit, server capabilities, and pass/fail cells. Never capture addresses, folder names, subjects, recipients, message bodies, credentials, protocol transcripts, or screenshots of private mail.

## Revoke and recover

Revoke the Mailflow-specific password at Apple Account to invalidate server access. Disconnecting the account in Mailflow removes the encrypted local credential but cannot revoke it remotely. After a primary-password change or manual revocation, disconnect and reconnect with a newly generated app-specific password.
