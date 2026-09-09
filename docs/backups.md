# Encrypted backups and restore

The [operator runbook](operator-runbook.md) defines when to capture and prove a
snapshot in the full install/update lifecycle. This document is the detailed
backup and restore contract.

Mailflow runs Restic in an isolated Compose service. The service creates a
consistent custom-format PostgreSQL dump, copies the immutable CDN tree into a
private staging directory, and includes the account-encryption master key in the
encrypted snapshot. It never reads PostgreSQL's live data directory and never
receives the Docker socket.

The default schedule is `03:00` UTC. A backup is recorded as successful only
after Restic has created the snapshot, verified repository data, and applied the
retention policy of 7 daily, 4 weekly, and 12 monthly snapshots. An interrupted,
partial, unverifiable, or unpruned run is recorded with a fixed error code and
appears in Admin as degraded or blocked.

## Local disk or NAS

Set `BACKUP_PATH` in `deploy/.env` to an absolute mounted NAS path or a local
directory outside the application volumes. Create `deploy/secrets/restic_password`
with a strong random value and keep a separate offline copy: neither Mailflow nor
the Restic snapshot can recover a lost repository password.

Start the scheduler and create an immediate snapshot with:

```sh
docker compose --env-file deploy/.env -f deploy/compose.yml --profile backup up -d backup
docker compose --env-file deploy/.env -f deploy/compose.yml --profile backup run --rm backup run
```

Change `MAILFLOW_BACKUP_SCHEDULE` and `MAILFLOW_BACKUP_TIMEZONE` only with an
`HH:MM` value and an IANA timezone. Disable scheduling with
`MAILFLOW_BACKUP_ENABLED=false`; the explicit `run` command remains available.

## S3-compatible repository

Set `RESTIC_REPOSITORY` to the Restic S3 URL, for example
`s3:https://storage.example.test/mailflow`. Mount the access key and secret key
as files in a private Compose override and set `AWS_ACCESS_KEY_ID_FILE` and
`AWS_SECRET_ACCESS_KEY_FILE` to those paths. Do not place either credential in
Compose, `.env`, command arguments, issue reports, or logs.

## Empty-environment restore drill

Restores are intentionally operator-driven. Keep the running instance stopped
and use a new, empty PostgreSQL database, an empty CDN directory, and an empty
target directory. Never point a restore at the live database or a non-empty CDN.

First restore and validate the snapshot without applying it:

```sh
mkdir -p restore
docker compose --env-file deploy/.env -f deploy/compose.yml --profile backup run --rm \
  -v "$PWD/restore:/restore" backup restore SNAPSHOT_ID /restore/snapshot
```

The command verifies the privacy-safe manifest, the PostgreSQL dump checksum,
and the presence of the encrypted account master key. A full isolated drill can
then be applied with a separate Compose project:

```sh
mkdir -p restore/apply restore/cdn
docker compose -p mailflow-restore --env-file deploy/.env -f deploy/compose.yml up -d postgres
docker compose -p mailflow-restore --env-file deploy/.env -f deploy/compose.yml exec postgres \
  pg_isready -U mailflow -d mailflow
docker compose -p mailflow-restore --env-file deploy/.env -f deploy/compose.yml --profile backup run --rm --no-deps \
  -e MAILFLOW_RESTORE_APPLY=true \
  -v "$PWD/restore/apply:/restore" -v "$PWD/restore/cdn:/data/cdn" \
  backup restore SNAPSHOT_ID /restore/snapshot
```

The service imports the empty database, restores CDN objects, and writes the
recovered master key to `/restore/master_key` with mode `0600`. Install that key
as the isolated instance's `deploy/secrets/master_key` before starting API or
worker. Use a private copy of the deploy directory for the drill so the live
secret is never overwritten.

After startup, verify the owner can sign in, accounts and mail metadata load,
cached attachments open, and Admin shows the restored scheduler state. Keep the
drill isolated until those checks pass. A dump checksum mismatch, missing master
key, non-empty destination, failed `pg_restore`, Restic check failure, or
retention failure must be treated as a failed restore or backup, never success.

## Privacy and recovery boundaries

Runtime status stores only timing, counts, repository kind, a Restic snapshot
identifier, and allowlisted error codes. Logs and Admin responses never contain
mail bodies, subjects, recipients, provider credentials, tokens, cookies,
signed URLs, object paths, repository passwords, or raw personal identifiers.
The staging and command-output limits are bounded, symlinks are rejected, and
temporary staging data is removed after each attempt.
