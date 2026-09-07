# Local secrets

Create `better_auth_secret`, `bootstrap_token`, `master_key`, `postgres_password`, and `restic_password` in this directory before starting Compose. Each file must contain only the secret value and must remain untracked. Generate the account-encryption key with `openssl rand -base64 32 > deploy/secrets/master_key`; keep this file with the backups because encrypted provider credentials cannot be recovered without it. The bootstrap token is accepted only while the Better Auth user table is empty.

On a fresh installation, open Mailflow and enter the `bootstrap_token` value in the first-run form together with the owner's name, email, password, and language. The browser sends it once in the `X-Mailflow-Bootstrap-Token` header; it is never placed in a URL or browser storage. After the owner exists, registration is closed at both the application and database layers, so the token cannot create another account.

Keep the secret files readable only by the account operating Docker. Do not paste their values into issues, logs, screenshots, shell history, or repository files.
