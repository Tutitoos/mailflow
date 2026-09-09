# Local secrets

Create `better_auth_secret`, `bootstrap_token`, `recovery_code`, `master_key`, `postgres_password`, `restic_password`, `google_oauth_client_secret`, and `microsoft_oauth_client_secret` in this directory before starting Compose. Each file must contain only the secret value and must remain untracked. Generate the account-encryption key with `openssl rand -base64 32 > deploy/secrets/master_key`; it is included inside each encrypted Restic snapshot because provider credentials cannot be recovered without it. Generate the recovery code with `openssl rand -base64 32 > deploy/secrets/recovery_code`, keep a paper or offline copy, and do not store it in a password field inside Mailflow. It resets the sole owner's password, removes registered passkeys, and revokes every active session. Keep the Restic password separately offline because it cannot be recovered from a snapshot. The bootstrap token is accepted only while the Better Auth user table is empty. Set the non-secret Google and Microsoft client IDs in `deploy/.env`; keep their client secrets only in the matching secret files.

On a fresh installation, open Mailflow and enter the `bootstrap_token` value in the first-run form together with the owner's name, email, password, and language. The browser sends it once in the `X-Mailflow-Bootstrap-Token` header; it is never placed in a URL or browser storage. After the owner exists, registration is closed at both the application and database layers, so the token cannot create another account.

Keep the secret files readable only by the account operating Docker. Do not paste their values into issues, logs, screenshots, shell history, or repository files.

Local Compose uses this directory by default. Production requires an absolute
`MAILFLOW_SECRETS_PATH` outside the checkout, a directory mode of `700`, and a
mode of `600` for every secret file; see
[`docs/deployment.md`](../../docs/deployment.md).
