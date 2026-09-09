# Native sessions

Mailflow desktop uses Better Auth as its only session authority. Successful password or passkey authentication returns a signed, revocable Better Auth session token to the trusted Tauri document. Tauri validates that token with the configured installation before saving it as a macOS Keychain generic-password item under `dev.tutitoos.mailflow.native-session`.

The refresh session never enters SQLite, preferences, URLs, logs, crash metadata, or persistent WebView storage. The WebView receives only a 15-minute JWT, kept in memory and renewed by a bounded Tauri command. Every command checks that the caller has the exact configured Mailflow origin. A separate random installation identifier also lives in Keychain and is sent only as non-secret request metadata.

Passkey sign-in keeps only the short WebAuthn challenge cookie in the WebView. Passkey registration is proxied through two allowlisted Tauri commands: Rust attaches the Keychain session, retains the challenge cookie only in memory between ceremony steps, and returns no refresh credential to JavaScript.

Signing out revokes the remote Better Auth session, removes its Keychain item, destroys every offline-cache encryption key, and clears the encrypted cache. If the remote service is unavailable, Mailflow does not report a successful logout; local recovery remains available.

## Recovery

Each installation has a `recovery_code` Docker Secret generated locally. The recovery form sends it only in a bounded JSON POST body over HTTPS. Five failed attempts lock recovery for 15 minutes. A successful recovery replaces the owner password, revokes every session, removes registered passkeys, clears browser authentication state, and asks the owner to sign in and register trusted passkeys again. Keep the recovery code and Restic password offline: neither can be recovered from Mailflow itself.

## Manual validation

1. Sign in on macOS and confirm that only the `refresh-session` and `installation-id` items exist in the native-session Keychain service.
2. Lock Keychain and confirm that Mailflow shows authentication unavailable without falling back to a stored refresh token.
3. Revoke the matching `auth_sessions` row and confirm the desktop returns to sign-in on its next refresh.
4. Sign out and confirm both the native session and encrypted offline cache become unavailable.
5. Recover with the offline code and confirm the old password and every old session fail while the new password succeeds.
6. Reinstall the app without deleting Keychain and confirm the same valid installation session can be resumed; then use account recovery to clear it.
