# Operator runbook

This is the canonical end-to-end runbook for a single-user Mailflow production
installation. Follow it from a verified source commit. Commands assume a Linux
host, the repository at `/srv/mailflow/source`, and private operator files below
`/srv/mailflow`. Replace every angle-bracket placeholder locally; never paste a
secret into a command, issue, log, screenshot, URL, or release record.

For the complete Spanish version, see [Guía de operaciones](es/operator-runbook.md).
Detailed contracts remain in [production deployment](deployment.md), [encrypted
backups](backups.md), and [supply-chain verification](supply-chain.md).

## 1. Record the recovery identities

Before changing a host, create an offline change record containing:

- the exact 40-character source commit;
- the release tag and the SHA-256 of its evidence file;
- the five verified Mailflow image manifest digests;
- the reviewed Traefik, PostgreSQL, and Redis manifest digests;
- the current Restic snapshot ID and its successful restore-drill date;
- the previous release environment path and database compatibility decision.

Do not continue without the previous record, the offline Restic password,
recovery code, and account-encryption master key. A tag alone is not an
identity. The environment record contains no credentials but is private because
it describes the installation.

## 2. Prepare the host and DNS

Install Docker Engine and Docker Compose 2.24.4 or newer on a supported Linux
host. Point the chosen DNS name to the host and allow inbound TCP 80 and 443.
Do not publish PostgreSQL, Redis, the Traefik dashboard, or the Docker socket.

Check out the reviewed source commit without creating a mutable branch:

```sh
sudo install -d -m 755 -o "$(id -un)" -g "$(id -gn)" /srv/mailflow/source
git clone https://github.com/Tutitoos/mailflow.git /srv/mailflow/source
cd /srv/mailflow/source
MAILFLOW_SOURCE_COMMIT='REPLACE_WITH_VERIFIED_40_HEX_COMMIT'
printf '%s\n' "$MAILFLOW_SOURCE_COMMIT" | grep -Eq '^[0-9a-f]{40}$'
git checkout --detach "$MAILFLOW_SOURCE_COMMIT"
test "$(git rev-parse HEAD)" = "$MAILFLOW_SOURCE_COMMIT"
```

Create private state directories. The backup path must be a mounted NAS/disk or
other location outside Mailflow's application volumes.

```sh
sudo install -d -m 700 -o "$(id -un)" -g "$(id -gn)" \
  /srv/mailflow/secrets /srv/mailflow/runtime /srv/mailflow/releases \
  /srv/mailflow/backups
```

## 3. Create secrets and the release environment

Generate secrets on the host with a private umask. The optional provider and
SMTP files still have to exist, but may remain empty until configured.

```sh
umask 077
openssl rand -base64 48 > /srv/mailflow/secrets/better_auth_secret
openssl rand -base64 32 > /srv/mailflow/secrets/bootstrap_token
openssl rand -base64 32 > /srv/mailflow/secrets/recovery_code
openssl rand -base64 32 > /srv/mailflow/secrets/master_key
openssl rand -base64 32 > /srv/mailflow/secrets/postgres_password
openssl rand -base64 32 > /srv/mailflow/secrets/restic_password
install -m 600 /dev/null /srv/mailflow/secrets/google_oauth_client_secret
install -m 600 /dev/null /srv/mailflow/secrets/microsoft_oauth_client_secret
install -m 600 /dev/null /srv/mailflow/secrets/alert_smtp_password
chmod 600 /srv/mailflow/secrets/*
```

Store offline copies of `recovery_code`, `master_key`, and `restic_password` in
separate custody. Losing the last two makes provider credentials or backups
unrecoverable. Copy the release template, replace every image with its verified
digest, set the real domain/email, and keep OAuth client IDs non-secret:

```sh
cp deploy/.env.production.example /srv/mailflow/releases/vX.Y.Z.env
chmod 600 /srv/mailflow/releases/vX.Y.Z.env
```

Set `MAILFLOW_SECRETS_PATH=/srv/mailflow/secrets`,
`MAILFLOW_TRAEFIK_CONFIG_PATH=/srv/mailflow/runtime/traefik-dynamic.yml`, and
`BACKUP_PATH=/srv/mailflow/backups`. Every image must use
`registry/repository@sha256:<64-lowercase-hex>`; do not use tags or `latest`.

## 4. Configure mail providers

Provider applications belong to this installation and use its exact HTTPS
domain. Leave a provider's client ID and secret empty when it is not required.

- **Google:** follow [Google OAuth setup](providers/google.md), set
  `GOOGLE_OAUTH_CLIENT_ID`, and write only the secret value to
  `/srv/mailflow/secrets/google_oauth_client_secret`. The redirect is
  `https://<mailflow-domain>/api/v1/oauth/google/callback`.
- **Microsoft:** follow [Microsoft OAuth setup](providers/microsoft.md), set
  `MICROSOFT_OAUTH_CLIENT_ID` and `MICROSOFT_OAUTH_AUTHORITY`, and write only
  the secret value to `/srv/mailflow/secrets/microsoft_oauth_client_secret`.
  The redirect is
  `https://<mailflow-domain>/api/v1/oauth/microsoft/callback`.
- **iCloud:** no server OAuth secret is required. Follow [iCloud Mail setup](providers/icloud.md)
  and generate an Apple app-specific password; never enter the primary Apple
  Account password.
- **Generic IMAP/SMTP:** follow [generic IMAP setup](providers/imap.md). Only
  verified TLS endpoints and an app-specific password are supported.

## 5. Validate, deploy, and bootstrap

The check is fail-closed and does not start containers. Resolve every error
instead of weakening permissions or replacing digests with tags.

```sh
cd /srv/mailflow/source
./scripts/production-compose.sh check /srv/mailflow/releases/vX.Y.Z.env
./scripts/production-compose.sh apply /srv/mailflow/releases/vX.Y.Z.env
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup ps
curl --fail --silent --show-error --head "https://<mailflow-domain>/"
```

Only Traefik may expose host ports. All required services must be healthy and
the migration must have exited successfully. Open the HTTPS origin, submit the
bootstrap token once with the owner's name, email, password, and language, then
sign out and back in. Registration closes after that owner exists.

Register at least one passkey, download no secret through the browser, and keep
the recovery code offline. Test recovery only in an isolated drill: success
changes the password, removes passkeys, and revokes all sessions.

Connect each configured provider from **Settings → Accounts**. Confirm initial
sync begins, send a sanitized test message, retrieve a test attachment, and
disconnect the test account. Protected validation must never use private mail
fixtures or screenshots.

## 6. Back up and prove restoration

Create a backup immediately after bootstrap and before every update:

```sh
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup \
  run --rm backup run
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup \
  run --rm --entrypoint restic backup snapshots --latest 1
```

Record the full snapshot ID. A successful backup is not sufficient until that
snapshot passes the [empty-environment restore drill](backups.md#empty-environment-restore-drill).
Use a separate Compose project, empty database, empty CDN, and private restore
directory. Never point a drill at the live database or live CDN.

After the isolated restore starts, verify sign-in, account/mail metadata, a
sanitized cached attachment, and Admin backup state. Then deliberately destroy
only the drill after recording success:

```sh
docker compose -p mailflow-restore --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup \
  down --volumes --remove-orphans
```

> **Destructive:** `down --volumes` irreversibly removes the named volumes of
> the selected Compose project. Confirm the project is exactly
> `mailflow-restore`; never run it against the live `mailflow` project.

## 7. Upgrade and roll back

Do not edit the active environment in place. Verify the new source commit and
release evidence, create a new `vX.Y.Z.env`, take and restore-test a backup,
read the migration compatibility statement, then run:

```sh
./scripts/production-compose.sh check /srv/mailflow/releases/vNEXT.env
./scripts/production-compose.sh apply /srv/mailflow/releases/vNEXT.env
```

Verify HTTPS, health, login, one provider sync, one sanitized message, one
attachment, Admin status, and backup scheduling. Keep the previous environment
and snapshot until the observation period ends.

For a compatible database, roll back the exact previous image set:

```sh
./scripts/production-compose.sh rollback /srv/mailflow/releases/vPREVIOUS.env
```

An image rollback never reverses a migration. If compatibility is not explicitly
guaranteed, stop and restore the matching previous snapshot into empty volumes;
do not reuse the migrated database. Record the resulting commit, image digests,
snapshot, time, and sanitized outcome.

## 8. Diagnose and recover

Use Admin status first, then bounded container state and JSON logs:

```sh
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup ps
docker compose --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml logs --since 15m --tail 200
```

Sanitize evidence before sharing it. Never include message content, addresses,
subjects, tokens, cookies, signed URLs, credentials, secret paths, or raw
provider responses. Do not enable the Traefik dashboard, mount the Docker
socket, run privileged containers, use `chmod 777`, or bypass TLS to diagnose.

Use the offline recovery code when the sole owner cannot authenticate. If a
provider grant is invalid, reconnect that account; if the master key is lost,
restore it from separately protected custody or a verified Restic snapshot.
If the Restic password is lost, the repository cannot be recovered.

## 9. Uninstall

Export the final change record and prove its snapshot in an isolated restore
before removing anything. First stop the live project without deleting data:

```sh
docker compose -p mailflow --env-file /srv/mailflow/releases/vX.Y.Z.env \
  -f deploy/compose.yml -f deploy/compose.production.yml --profile backup down
```

Revoke Google and Microsoft grants, revoke the iCloud app-specific password,
and retain the release records, Restic repository, and offline keys according
to the owner's retention decision. Removing `/srv/mailflow` or Docker volumes
is deliberately not provided as a copy-paste command.

> **Destructive:** deleting application volumes removes the only live database,
> CDN cache, Redis state, and ACME state. It is authorized only after the exact
> targets and recovery snapshot have been independently verified.
