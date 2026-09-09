#!/usr/bin/env bash
set -euo pipefail

readonly repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly drill_root="$(mktemp -d "${TMPDIR:-/tmp}/mailflow-operator-drill.XXXXXX")"
readonly secrets_dir="$drill_root/secrets"
readonly runtime_dir="$drill_root/runtime"
readonly releases_dir="$drill_root/releases"
readonly backups_dir="$drill_root/backups"
readonly digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

cleanup() {
  find "$drill_root" -type f -delete 2>/dev/null || true
  find "$drill_root" -depth -type d -exec rmdir {} \; 2>/dev/null || true
}
trap cleanup EXIT

install -d -m 700 "$secrets_dir" "$runtime_dir" "$releases_dir" "$backups_dir"
for name in better_auth_secret bootstrap_token recovery_code master_key postgres_password restic_password; do
  printf '%s' "operator-drill-$name" > "$secrets_dir/$name"
  chmod 600 "$secrets_dir/$name"
done
for name in google_oauth_client_secret microsoft_oauth_client_secret alert_smtp_password; do
  install -m 600 /dev/null "$secrets_dir/$name"
done

write_release() {
  local path="$1" domain="$2"
  {
    printf 'MAILFLOW_DOMAIN=%s\n' "$domain"
    printf 'ACME_EMAIL=operator@example.test\n'
    printf 'MAILFLOW_TRAEFIK_IMAGE=docker.io/library/traefik@sha256:%s\n' "$digest"
    printf 'MAILFLOW_WEB_IMAGE=ghcr.io/tutitoos/mailflow-web@sha256:%s\n' "$digest"
    printf 'MAILFLOW_AUTH_IMAGE=ghcr.io/tutitoos/mailflow-auth@sha256:%s\n' "$digest"
    printf 'MAILFLOW_API_IMAGE=ghcr.io/tutitoos/mailflow-api@sha256:%s\n' "$digest"
    printf 'MAILFLOW_WORKER_IMAGE=ghcr.io/tutitoos/mailflow-worker@sha256:%s\n' "$digest"
    printf 'MAILFLOW_BACKUP_IMAGE=ghcr.io/tutitoos/mailflow-backup@sha256:%s\n' "$digest"
    printf 'MAILFLOW_POSTGRES_IMAGE=docker.io/library/postgres@sha256:%s\n' "$digest"
    printf 'MAILFLOW_REDIS_IMAGE=docker.io/library/redis@sha256:%s\n' "$digest"
    printf 'MAILFLOW_SECRETS_PATH=%s\n' "$secrets_dir"
    printf 'MAILFLOW_TRAEFIK_CONFIG_PATH=%s\n' "$runtime_dir/traefik-dynamic.yml"
    printf 'BACKUP_PATH=%s\n' "$backups_dir"
    printf 'RESTIC_REPOSITORY=/repository\n'
  } > "$path"
  chmod 600 "$path"
}

write_release "$releases_dir/vCURRENT.env" current.example.test
write_release "$releases_dir/vPREVIOUS.env" previous.example.test

cd "$repository_root"
./scripts/production-compose.sh check "$releases_dir/vCURRENT.env"
grep -q 'current.example.test' "$runtime_dir/traefik-dynamic.yml"
! grep -q '__MAILFLOW_DOMAIN__' "$runtime_dir/traefik-dynamic.yml"

./scripts/production-compose.sh check "$releases_dir/vPREVIOUS.env"
grep -q 'previous.example.test' "$runtime_dir/traefik-dynamic.yml"
! grep -q 'current.example.test' "$runtime_dir/traefik-dynamic.yml"

test "$(git rev-parse HEAD | wc -c | tr -d ' ')" = "41"
test "$(find "$secrets_dir" -type f | wc -l | tr -d ' ')" = "9"
printf '%s\n' "operator runbook drill passed with isolated release records"
