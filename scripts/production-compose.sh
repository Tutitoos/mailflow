#!/usr/bin/env bash
set -euo pipefail

readonly repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly base_compose="$repository_root/deploy/compose.yml"
readonly production_compose="$repository_root/deploy/compose.production.yml"
readonly traefik_template="$repository_root/deploy/traefik-dynamic.yml.template"

fail() {
  echo "production deployment rejected: $*" >&2
  exit 1
}

env_value() {
  local key="$1" file="$2"
  awk -F= -v key="$key" '$1 == key { sub(/^[^=]*=/, ""); print; found=1 } END { if (!found) exit 1 }' "$file"
}

file_mode() {
  if [[ "$(uname -s)" == "Darwin" ]]; then
    stat -f '%Lp' "$1"
  else
    stat -c '%a' "$1"
  fi
}

validate_private_file() {
  local file="$1" allow_empty="${2:-false}" mode
  [[ -f "$file" && ! -L "$file" ]] || fail "$file must be a regular file"
  mode="$(file_mode "$file")"
  [[ "$mode" == "600" ]] || fail "$file must have mode 600"
  [[ "$allow_empty" == "true" || -s "$file" ]] || fail "$file must not be empty"
}

validate_image() {
  local key="$1" expected="$2" env_file="$3" value
  value="$(env_value "$key" "$env_file")" || fail "$key is missing"
  [[ "$value" =~ ^${expected}@sha256:[a-f0-9]{64}$ ]] || fail "$key must be an immutable $expected digest reference"
}

validate_inputs() {
  local env_file="$1" mode secrets_dir traefik_config traefik_config_dir
  command -v docker >/dev/null || fail "docker is required"
  docker compose version >/dev/null || fail "Docker Compose is required"
  validate_private_file "$env_file"

  secrets_dir="$(env_value MAILFLOW_SECRETS_PATH "$env_file")" || fail "MAILFLOW_SECRETS_PATH is missing"
  [[ "$secrets_dir" == /* ]] || fail "MAILFLOW_SECRETS_PATH must be absolute"
  [[ -d "$secrets_dir" && ! -L "$secrets_dir" ]] || fail "$secrets_dir must be a directory"
  mode="$(file_mode "$secrets_dir")"
  [[ "$mode" == "700" ]] || fail "$secrets_dir must have mode 700"
  validate_private_file "$secrets_dir/better_auth_secret"
  validate_private_file "$secrets_dir/bootstrap_token"
  validate_private_file "$secrets_dir/recovery_code"
  validate_private_file "$secrets_dir/postgres_password"
  validate_private_file "$secrets_dir/restic_password"
  validate_private_file "$secrets_dir/master_key"
  validate_private_file "$secrets_dir/google_oauth_client_secret" true
  validate_private_file "$secrets_dir/microsoft_oauth_client_secret" true
  validate_private_file "$secrets_dir/alert_smtp_password" true

  traefik_config="$(env_value MAILFLOW_TRAEFIK_CONFIG_PATH "$env_file")" \
    || fail "MAILFLOW_TRAEFIK_CONFIG_PATH is missing"
  [[ "$traefik_config" == /* ]] || fail "MAILFLOW_TRAEFIK_CONFIG_PATH must be absolute"
  traefik_config_dir="$(dirname "$traefik_config")"
  [[ -d "$traefik_config_dir" && ! -L "$traefik_config_dir" ]] \
    || fail "$traefik_config_dir must be a directory"
  [[ "$(file_mode "$traefik_config_dir")" == "700" ]] \
    || fail "$traefik_config_dir must have mode 700"
  [[ ! -e "$traefik_config" || ( -f "$traefik_config" && ! -L "$traefik_config" ) ]] \
    || fail "$traefik_config must be a regular file"

  validate_image MAILFLOW_TRAEFIK_IMAGE '(docker\.io/library/)?traefik' "$env_file"
  validate_image MAILFLOW_WEB_IMAGE 'ghcr\.io/tutitoos/mailflow-web' "$env_file"
  validate_image MAILFLOW_AUTH_IMAGE 'ghcr\.io/tutitoos/mailflow-auth' "$env_file"
  validate_image MAILFLOW_API_IMAGE 'ghcr\.io/tutitoos/mailflow-api' "$env_file"
  validate_image MAILFLOW_WORKER_IMAGE 'ghcr\.io/tutitoos/mailflow-worker' "$env_file"
  validate_image MAILFLOW_BACKUP_IMAGE 'ghcr\.io/tutitoos/mailflow-backup' "$env_file"
  validate_image MAILFLOW_POSTGRES_IMAGE '(docker\.io/library/)?postgres' "$env_file"
  validate_image MAILFLOW_REDIS_IMAGE '(docker\.io/library/)?redis' "$env_file"

  env_value MAILFLOW_DOMAIN "$env_file" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$' \
    || fail "MAILFLOW_DOMAIN must be a DNS hostname"
  env_value ACME_EMAIL "$env_file" | grep -Eq '^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$' \
    || fail "ACME_EMAIL must be an email address"
}

render_traefik_config() {
  local env_file="$1" domain destination directory temporary
  domain="$(env_value MAILFLOW_DOMAIN "$env_file")"
  destination="$(env_value MAILFLOW_TRAEFIK_CONFIG_PATH "$env_file")"
  directory="$(dirname "$destination")"
  temporary="$(mktemp "$directory/.traefik-dynamic.XXXXXX")"
  trap 'rm -f -- "$temporary"' RETURN
  sed "s/__MAILFLOW_DOMAIN__/$domain/g" "$traefik_template" > "$temporary"
  # The file contains routes and limits, never credentials. Traefik runs as a
  # non-root user and Compose file-backed configs preserve host readability.
  chmod 644 "$temporary"
  mv -- "$temporary" "$destination"
  trap - RETURN
}

run_compose() {
  local env_file="$1"
  shift
  docker compose --project-name mailflow --env-file "$env_file" \
    -f "$base_compose" -f "$production_compose" --profile backup "$@"
}

clear_compose_overrides() {
  local key
  local -a keys=(
    MAILFLOW_DOMAIN ACME_EMAIL MAILFLOW_TRAEFIK_IMAGE MAILFLOW_WEB_IMAGE
    MAILFLOW_AUTH_IMAGE MAILFLOW_API_IMAGE MAILFLOW_WORKER_IMAGE MAILFLOW_BACKUP_IMAGE
    MAILFLOW_POSTGRES_IMAGE MAILFLOW_REDIS_IMAGE MAILFLOW_SECRETS_PATH
    MAILFLOW_TRAEFIK_CONFIG_PATH BACKUP_PATH
    RESTIC_REPOSITORY MAILFLOW_BACKUP_ENABLED MAILFLOW_BACKUP_SCHEDULE
    MAILFLOW_BACKUP_TIMEZONE GOOGLE_OAUTH_CLIENT_ID MICROSOFT_OAUTH_CLIENT_ID
    MICROSOFT_OAUTH_AUTHORITY VITE_SENTRY_DSN MAILFLOW_SENTRY_REPLAY_ENABLED
    MAILFLOW_ALERT_SMTP_HOST MAILFLOW_ALERT_SMTP_PORT MAILFLOW_ALERT_SMTP_USERNAME
    MAILFLOW_ALERT_SMTP_FROM MAILFLOW_ALERT_SMTP_TO MAILFLOW_ALERT_SMTP_IMPLICIT_TLS
    MAILFLOW_ALERT_CONNECTED_ACCOUNT_FALLBACK MAILFLOW_IMAP_ACCOUNT_REFRESH
    MAILFLOW_IMAP_IDLE_HEARTBEAT MAILFLOW_IMAP_POLL_INTERVAL MAILFLOW_IMAP_RECONNECT_MIN
    MAILFLOW_IMAP_RECONNECT_MAX MAILFLOW_IMAP_LEASE_RENEW MAILFLOW_IMAP_BURST_WINDOW
    MAILFLOW_IMAP_MAX_CONNECTIONS
  )
  for key in "${keys[@]}"; do
    unset "$key"
  done
}

usage() {
  echo "usage: $0 check ENV_FILE | apply ENV_FILE | rollback PREVIOUS_ENV_FILE" >&2
  exit 2
}

[[ "$#" == "2" ]] || usage
readonly action="$1"
readonly release_env_file="$2"
validate_inputs "$release_env_file"
render_traefik_config "$release_env_file"
clear_compose_overrides
run_compose "$release_env_file" config --quiet

case "$action" in
  check) ;;
  apply|rollback)
    run_compose "$release_env_file" pull
    run_compose "$release_env_file" up -d --no-build --wait --wait-timeout 300
    ;;
  *) usage ;;
esac
