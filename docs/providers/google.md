# Google OAuth setup

Mailflow uses a Google OAuth client owned by each self-hosted installation. It never ships a shared client ID or secret.

1. Create a project in Google Cloud Console and configure its OAuth consent screen for the protected test users that may connect to this installation.
2. Enable the Gmail API.
3. Create a **Web application** OAuth client.
4. Add exactly `https://<your-mailflow-domain>/api/v1/oauth/google/callback` as an authorized redirect URI. Local development may use `http://127.0.0.1:<port>/api/v1/oauth/google/callback`.
5. Set `GOOGLE_OAUTH_CLIENT_ID` in `deploy/.env` and write the client secret alone to `deploy/secrets/google_oauth_client_secret`. Never commit either installation file.
6. Restart the API and worker, then open **Settings → Accounts**. The capability status should change from setup guidance to **Connect Google**. Both services need the OAuth configuration: the API handles consent and the worker renews short-lived access tokens during background synchronization.

Mailflow requests OpenID identity, email identity, and `gmail.modify`. Authorization uses PKCE S256 and a random state stored in Redis for ten minutes. State is consumed atomically, so callback replay fails. Google tokens are encrypted through the account vault before PostgreSQL receives them and are never returned by the API.

Disconnecting always disables local access. Mailflow also asks Google to revoke the refresh token and reports only whether that remote request succeeded. Use **Request consent again** if Google did not return a new refresh token or the installation scopes changed.

## Gmail mapping

The Gmail adapter keeps every `message.id` and `threadId` inside its Mailflow account boundary. `INBOX`, `SENT`, `DRAFT`, `TRASH`, and `SPAM` map to mailbox roles. `CATEGORY_PERSONAL`, `CATEGORY_PROMOTIONS`, `CATEGORY_SOCIAL`, `CATEGORY_UPDATES`, and `CATEGORY_FORUMS` map to the five local categories; all remaining Google system labels and user labels retain their remote identity without being reinterpreted.

Initial pages use Gmail message-list page tokens and incremental pages use History IDs plus page tokens. Message payloads pass through Mailflow's bounded MIME normalizer and HTML sanitizer before reaching the domain. Attachment IDs are resolved from the full Gmail payload and their bytes remain on-demand.

The authenticated attachment endpoint uses that stable domain ID to recover a missing or expired Gmail part into the local CDN. Concurrent requests share one recovery operation, cache hits do not call Gmail, and cancellation propagates through the provider request and atomic write.

Provider responses are reduced to four stable error kinds: `authorization`, `quota`, `transient`, and `permanent`. Retry hints are retained as a duration, while response bodies and Google error messages are discarded so they cannot enter logs, events, or API errors.

## Synchronization lifecycle

Completing OAuth queues a durable initial run. Mailflow snapshots the Gmail History ID, synchronizes the newest 90 days first, then continues backward through the older mailbox before entering incremental History polling. Every provider page and its checkpoint commit in one PostgreSQL transaction; repeated queue delivery therefore updates the same account-scoped records instead of duplicating them.

Incremental polling runs every two minutes while the client is active and every ten minutes while idle. A manual `POST /api/v1/accounts/{accountId}/sync` request starts an immediate reconciliation and requires an `Idempotency-Key`. A full recent-window reconciliation is scheduled every 24 hours after the historical pass completes.

History pages include additions, label changes, and deletions. Label changes reload the canonical message state; deletions soft-delete the local message and refresh its thread summary. A `404` from the History endpoint marks only that incremental run as expired and queues one bounded recent-window recovery from a fresh History snapshot. Progress is published through the resumable `sync.progress` event stream without message metadata or provider credentials.
