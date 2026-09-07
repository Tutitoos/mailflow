# Google OAuth setup

Mailflow uses a Google OAuth client owned by each self-hosted installation. It never ships a shared client ID or secret.

1. Create a project in Google Cloud Console and configure its OAuth consent screen for the protected test users that may connect to this installation.
2. Enable the Gmail API.
3. Create a **Web application** OAuth client.
4. Add exactly `https://<your-mailflow-domain>/api/v1/oauth/google/callback` as an authorized redirect URI. Local development may use `http://127.0.0.1:<port>/api/v1/oauth/google/callback`.
5. Set `GOOGLE_OAUTH_CLIENT_ID` in `deploy/.env` and write the client secret alone to `deploy/secrets/google_oauth_client_secret`. Never commit either installation file.
6. Restart the API and open **Settings → Accounts**. The capability status should change from setup guidance to **Connect Google**.

Mailflow requests OpenID identity, email identity, and `gmail.modify`. Authorization uses PKCE S256 and a random state stored in Redis for ten minutes. State is consumed atomically, so callback replay fails. Google tokens are encrypted through the account vault before PostgreSQL receives them and are never returned by the API.

Disconnecting always disables local access. Mailflow also asks Google to revoke the refresh token and reports only whether that remote request succeeded. Use **Request consent again** if Google did not return a new refresh token or the installation scopes changed.
