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
