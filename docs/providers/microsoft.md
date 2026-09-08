# Microsoft OAuth setup

Mailflow uses a Microsoft Entra application owned by each self-hosted installation. It supports Outlook.com and Microsoft 365 accounts through the authorization-code flow with PKCE; Mailflow never ships a shared application ID or client secret.

## Register the application

1. In the Microsoft Entra admin center, create an app registration for the account types this installation should accept.
2. Add a **Web** redirect URI matching `https://<your-mailflow-domain>/api/v1/oauth/microsoft/callback` exactly. Local development may use `http://127.0.0.1:<port>/api/v1/oauth/microsoft/callback`.
3. Add these delegated Microsoft Graph permissions: `User.Read`, `Mail.ReadWrite`, and `Mail.Send`. Mailflow also requests the standard `openid`, `profile`, `email`, and `offline_access` scopes at runtime.
4. Create a client secret. Set the non-secret application ID in `MICROSOFT_OAUTH_CLIENT_ID` and write only the secret value to `deploy/secrets/microsoft_oauth_client_secret`.
5. Select `MICROSOFT_OAUTH_AUTHORITY`: `common` accepts personal and organizational accounts, `consumers` accepts Outlook.com accounts, `organizations` accepts work/school accounts, and a tenant UUID restricts access to one organization.

The app registration's supported account types must agree with the selected authority. Some Microsoft 365 tenants disable user consent; their administrator must approve the delegated permissions before an owner can connect that account.

## Consent and disconnect behavior

Mailflow stores refresh tokens encrypted with the installation master key. Requesting consent again opens the Microsoft consent screen explicitly. A revoked grant or an interaction-required refresh places the local account in an error state and asks the owner to reconnect.

Microsoft does not expose a minimum-scope endpoint that revokes only this app's refresh token. Disconnect therefore disables local access and deletes no provider mail; the owner can additionally remove the grant from [My Apps](https://myapps.microsoft.com/). Mailflow deliberately does not request the powerful permission that revokes all of a user's sign-in sessions.

Protocol details and valid authorities are documented in Microsoft's [authorization-code flow](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-auth-code-flow), and mail permission behavior is described by the [Microsoft Graph mail API](https://learn.microsoft.com/en-us/graph/api/resources/mail-api-overview?view=graph-rest-1.0).

## Microsoft Graph mapping

The Graph adapter requests `Prefer: IdType="ImmutableId"` on every operation. Message IDs therefore remain stable when a message moves between folders in the same mailbox; `conversationId` remains the thread key, and neither identifier is shared across Mailflow accounts. The adapter follows the complete opaque `@odata.nextLink` supplied by Graph and rejects a continuation URL whose scheme, host, or `/v1.0/me` scope differs from the configured endpoint.

Graph's v1.0 `mailFolder` object does not identify the well-known role of a returned folder. Mailflow resolves the localized-independent `inbox`, `sentitems`, `drafts`, `deleteditems`, `junkemail`, and `archive` routes first and maps the listed folder IDs against them. Other folders retain their remote identity without being assigned a system role. Outlook categories are keyed by their unique display name because message resources apply categories by display name; they remain provider labels and are not reinterpreted as Mailflow's five automatic inbox categories.

Backfill pages select only the metadata required for local state and fetch each message's MIME representation through `/$value`. The same bounded MIME normalizer and HTML sanitizer used by Gmail processes the content. Attachment metadata is matched to the sanitized MIME parts, while attachment bytes remain on-demand through the attachment `/$value` endpoint.

Read, flag, importance, category, move-to-Trash, restore-to-Inbox, and archive mutations use idempotent `PATCH` or immutable-ID move operations. A conversation action resolves all message IDs through paginated Graph results before applying the mutation. Category updates read the current category set and merge it case-insensitively, so adding or removing one category never replaces unrelated Outlook categories.

Draft and send contracts use Graph's documented base64 MIME input. Mailflow creates a draft first and then sends that immutable draft ID, which becomes the identifier of the Sent Items copy. Replacing a MIME draft creates the new authoritative draft before attempting to delete the previous one; reconciliation may remove an old orphan if that cleanup fails. Connecting these adapters to shared actions, drafts, attachments, and delivery remains a later Phase 5 unit.

Provider failures are reduced to `authorization`, `quota`, `transient`, or `permanent`. Machine-readable Graph error codes are accepted only when bounded to a safe character set, `Retry-After` is retained for scheduling, and provider messages or response bodies are never exposed. The repository contract suite uses sanitized Outlook.com and Microsoft 365 fixtures; real-account validation must run manually with protected installation credentials.

## Delta synchronization

The worker starts Microsoft synchronization as soon as the OAuth callback creates or reconnects an account. It commits the recursively paginated folder catalog first, then backfills the latest 90 days before older history. Each completed historical pass transitions to folder-scoped Microsoft Delta.

Mailflow retains the complete opaque `deltaLink` for every folder and follows `nextLink` pages without reconstructing their tokens. At the end of a round it traverses the folder hierarchy again so newly created folders receive their own baseline and removed folders stop polling. A tombstone can also mean that a message moved out of a folder; Mailflow therefore resolves the immutable message ID across the mailbox before deciding whether to upsert its new state or soft-delete it locally.

An HTTP 410, `SyncStateNotFound`, or `ResyncRequired` response cancels only the stale incremental run and starts a bounded recent-first recovery. Other provider failures leave the durable checkpoint unchanged and use the shared queue's bounded exponential retry. `Retry-After` remains available in the typed provider error, while no Graph response message, token, address, subject, or body enters logs, metrics, progress events, or dead letters.

Active accounts poll every two minutes, idle accounts every ten minutes, and a recent reconciliation runs every 24 hours. `sync.progress` reports only internal account/run identifiers, phase, state, and applied counts. Metrics identify the provider as `microsoft` without using account or message identifiers as dimensions.

The automated suite covers recursive folders, repeated checkpoints, `nextLink` and `deltaLink`, moves versus deletions, cursor expiry, throttling, partial-page failure, OAuth-triggered initial sync, adaptive schedules, and reconciliation with sanitized Outlook.com and Microsoft 365 fixtures. A protected real-account check remains manual because repository and CI environments must not contain installation credentials.
