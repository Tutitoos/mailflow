#!/usr/bin/env bash
set -euo pipefail

readonly image_names=(api auth backup web worker)
acceptance_temp_dir=""
acceptance_override=""
acceptance_project=""

fail() {
  echo "container verification failed: $*" >&2
  exit 1
}

inspect_local_images() {
  local name image container listing version architecture expected_architecture
  expected_architecture="$(docker version --format '{{.Server.Arch}}')"
  for name in "${image_names[@]}"; do
    image="mailflow-${name}:acceptance"
    docker image inspect "$image" >/dev/null
    version="$(docker image inspect "$image" --format '{{ index .Config.Labels "org.opencontainers.image.version" }}')"
    [[ "$version" =~ ^0\.0\.0-(local|pr\.[0-9]+)$ ]] || fail "$image has an invalid version label"
    architecture="$(docker image inspect "$image" --format '{{.Architecture}}')"
    test "$architecture" = "$expected_architecture" || fail "$image is not native for $expected_architecture"

    container="$(docker create "$image")"
    listing="$(docker export "$container" | tar -tf -)"
    docker rm "$container" >/dev/null
    if grep -E '(^|/)(\.git|deploy/secrets|id_rsa|id_ed25519)(/|$)' <<<"$listing" >/dev/null; then
      fail "$image contains repository metadata or secret material"
    fi
  done
}

wait_for_health() {
  local -a compose=("${@:1:$#-1}")
  local service="${!#}" container status
  container="$("${compose[@]}" ps -q "$service")"
  test -n "$container" || fail "$service was not created"
  for _ in {1..60}; do
    status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container")"
    case "$status" in
      healthy|running) return ;;
      exited|dead|unhealthy) fail "$service entered state $status" ;;
    esac
    sleep 2
  done
  fail "$service did not become healthy"
}

wait_for_completion() {
  local -a compose=("${@:1:$#-1}")
  local service="${!#}" container status exit_code
  container="$("${compose[@]}" ps -a -q "$service")"
  test -n "$container" || fail "$service was not created"
  for _ in {1..60}; do
    status="$(docker inspect --format '{{.State.Status}}' "$container")"
    if [[ "$status" == "exited" ]]; then
      exit_code="$(docker inspect --format '{{.State.ExitCode}}' "$container")"
      test "$exit_code" = "0" || fail "$service exited with code $exit_code"
      return
    fi
    [[ "$status" != "dead" ]] || fail "$service entered state $status"
    sleep 2
  done
  fail "$service did not complete"
}

cleanup_acceptance() {
  local status=$?
  if [[ -n "$acceptance_project" && -n "$acceptance_override" ]]; then
    if (( status != 0 )); then
      docker compose --project-name "$acceptance_project" --env-file deploy/.env.example \
        -f deploy/compose.yml -f "$acceptance_override" --profile backup ps -a >&2 || true
      docker compose --project-name "$acceptance_project" --env-file deploy/.env.example \
        -f deploy/compose.yml -f "$acceptance_override" --profile backup logs --no-color --tail 100 >&2 || true
    fi
    docker compose --project-name "$acceptance_project" --env-file deploy/.env.example \
      -f deploy/compose.yml -f "$acceptance_override" --profile backup down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  if [[ -n "$acceptance_temp_dir" && "$acceptance_temp_dir" == "${TMPDIR:-/tmp}"/mailflow-container-acceptance.* ]]; then
    rm -rf -- "$acceptance_temp_dir"
  fi
  return "$status"
}

verify_stack_health() {
  acceptance_temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/mailflow-container-acceptance.XXXXXX")"
  acceptance_override="$acceptance_temp_dir/secrets.yml"
  acceptance_project="mailflow_acceptance_${GITHUB_RUN_ID:-$$}"
  acceptance_project="${acceptance_project//[^a-zA-Z0-9_-]/_}"
  trap cleanup_acceptance EXIT

  mkdir -p "$acceptance_temp_dir/secrets" "$acceptance_temp_dir/repository"
  printf '%s' 'acceptance-auth-secret-0123456789abcdef' > "$acceptance_temp_dir/secrets/better_auth_secret"
  printf '%s' 'acceptance-bootstrap-token' > "$acceptance_temp_dir/secrets/bootstrap_token"
  printf '%s' 'acceptance-recovery-code' > "$acceptance_temp_dir/secrets/recovery_code"
  printf '%s' 'acceptance-postgres-password' > "$acceptance_temp_dir/secrets/postgres_password"
  printf '%s' 'acceptance-restic-password' > "$acceptance_temp_dir/secrets/restic_password"
  printf '%s' 'YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=' > "$acceptance_temp_dir/secrets/master_key"
  : > "$acceptance_temp_dir/secrets/google_oauth_client_secret"
  : > "$acceptance_temp_dir/secrets/microsoft_oauth_client_secret"
  : > "$acceptance_temp_dir/secrets/alert_smtp_password"
  chmod 600 "$acceptance_temp_dir/secrets/"*

  printf '%s\n' \
    'secrets:' \
    "  better_auth_secret: { file: $acceptance_temp_dir/secrets/better_auth_secret }" \
    "  bootstrap_token: { file: $acceptance_temp_dir/secrets/bootstrap_token }" \
    "  recovery_code: { file: $acceptance_temp_dir/secrets/recovery_code }" \
    "  postgres_password: { file: $acceptance_temp_dir/secrets/postgres_password }" \
    "  restic_password: { file: $acceptance_temp_dir/secrets/restic_password }" \
    "  master_key: { file: $acceptance_temp_dir/secrets/master_key }" \
    "  google_oauth_client_secret: { file: $acceptance_temp_dir/secrets/google_oauth_client_secret }" \
    "  microsoft_oauth_client_secret: { file: $acceptance_temp_dir/secrets/microsoft_oauth_client_secret }" \
    "  alert_smtp_password: { file: $acceptance_temp_dir/secrets/alert_smtp_password }" \
    > "$acceptance_override"

  export MAILFLOW_DOMAIN=mailflow.test
  export BACKUP_PATH="$acceptance_temp_dir/repository"
  export MAILFLOW_WEB_IMAGE="${MAILFLOW_WEB_IMAGE:-mailflow-web:acceptance}"
  export MAILFLOW_AUTH_IMAGE="${MAILFLOW_AUTH_IMAGE:-mailflow-auth:acceptance}"
  export MAILFLOW_API_IMAGE="${MAILFLOW_API_IMAGE:-mailflow-api:acceptance}"
  export MAILFLOW_WORKER_IMAGE="${MAILFLOW_WORKER_IMAGE:-mailflow-worker:acceptance}"
  export MAILFLOW_BACKUP_IMAGE="${MAILFLOW_BACKUP_IMAGE:-mailflow-backup:acceptance}"
  local compose=(docker compose --project-name "$acceptance_project" --env-file deploy/.env.example -f deploy/compose.yml -f "$acceptance_override" --profile backup)
  "${compose[@]}" config --quiet
  "${compose[@]}" up -d --no-build postgres redis
  for service in postgres redis; do
    wait_for_health "${compose[@]}" "$service"
  done
  "${compose[@]}" up -d --no-build migrate
  wait_for_completion "${compose[@]}" migrate
  "${compose[@]}" up -d --no-build --no-deps auth
  wait_for_health "${compose[@]}" auth
  "${compose[@]}" up -d --no-build --no-deps api
  wait_for_health "${compose[@]}" api
  "${compose[@]}" up -d --no-build --no-deps worker web backup
  for service in worker web backup; do
    wait_for_health "${compose[@]}" "$service"
  done
  "${compose[@]}" exec -T web wget -qO- http://127.0.0.1:8080/health/live >/dev/null
  "${compose[@]}" exec -T auth bun -e "fetch('http://127.0.0.1:3001/health/ready').then(r=>{if(!r.ok)process.exit(1)})"
  "${compose[@]}" exec -T api /usr/local/bin/api --healthcheck
  sleep 3
  for service in worker backup; do
    wait_for_health "${compose[@]}" "$service"
  done

  # Keep the restart fixture synthetic and unavailable to provider workers. It
  # represents one committed local action and its matching provider cursor.
  "${compose[@]}" exec -T postgres psql --username mailflow --dbname mailflow --set ON_ERROR_STOP=1 <<'SQL' >/dev/null
INSERT INTO users (id, email, name)
VALUES ('0199ed3b-c950-7000-8000-000000000073', 'release-acceptance@example.test', 'Release acceptance')
ON CONFLICT (id) DO NOTHING;
INSERT INTO accounts (id, user_id, provider, remote_id, display_name, encrypted_credentials, credential_nonce, capabilities, sync_state)
VALUES (
  '0199ed3b-c950-7000-8000-000000000173',
  '0199ed3b-c950-7000-8000-000000000073',
  'google', 'release-acceptance', 'Release acceptance', '\x01', '\x02', '{}', 'idle'
)
ON CONFLICT (id) DO NOTHING;
INSERT INTO sync_cursors (account_id, kind, cursor, checkpoint, version, last_success_at)
VALUES (
  '0199ed3b-c950-7000-8000-000000000173',
  'google_history', '{"historyId":"73"}', 73, 2, '2026-09-09T00:00:00Z'
)
ON CONFLICT (account_id) DO UPDATE SET
  cursor = EXCLUDED.cursor,
  checkpoint = EXCLUDED.checkpoint,
  version = EXCLUDED.version,
  last_success_at = EXCLUDED.last_success_at;
INSERT INTO pending_actions (
  id, account_id, idempotency_key, kind, desired_state, status, attempts,
  available_at, target_kind, target_id, max_attempts
)
VALUES (
  '0199ed3b-c950-7000-8000-000000000273',
  '0199ed3b-c950-7000-8000-000000000173',
  'release-acceptance-73', 'archive', '{}', 'retry_wait', 1,
  '2099-01-01T00:00:00Z', 'thread',
  '0199ed3b-c950-7000-8000-000000000373', 5
)
ON CONFLICT (account_id, idempotency_key) DO UPDATE SET
  status = EXCLUDED.status,
  attempts = EXCLUDED.attempts,
  available_at = EXCLUDED.available_at;
SQL
  "${compose[@]}" exec -T redis redis-cli SET mailflow:acceptance:restart-marker committed >/dev/null

  # API and worker are process-restart contracts. PostgreSQL and Redis are
  # stopped separately afterwards so their durable state can be inspected
  # before application processes are allowed to reconnect.
  "${compose[@]}" restart api worker >/dev/null
  for service in api worker; do
    wait_for_health "${compose[@]}" "$service"
  done
  "${compose[@]}" stop api worker >/dev/null
  "${compose[@]}" restart postgres redis >/dev/null
  for service in postgres redis; do
    wait_for_health "${compose[@]}" "$service"
  done

  local cursor_state action_state redis_state
  cursor_state="$("${compose[@]}" exec -T postgres psql --username mailflow --dbname mailflow --tuples-only --no-align --command \
    "SELECT cursor->>'historyId' || ':' || checkpoint || ':' || version FROM sync_cursors WHERE account_id = '0199ed3b-c950-7000-8000-000000000173'")"
  action_state="$("${compose[@]}" exec -T postgres psql --username mailflow --dbname mailflow --tuples-only --no-align --command \
    "SELECT status || ':' || attempts FROM pending_actions WHERE id = '0199ed3b-c950-7000-8000-000000000273'")"
  redis_state="$("${compose[@]}" exec -T redis redis-cli --raw GET mailflow:acceptance:restart-marker)"
  test "$cursor_state" = "73:73:2" || fail "provider cursor changed across the PostgreSQL restart"
  test "$action_state" = "retry_wait:1" || fail "committed action changed across the PostgreSQL restart"
  test "$redis_state" = "committed" || fail "Redis restart marker was not durable"

  "${compose[@]}" start api worker >/dev/null
  for service in api worker; do
    wait_for_health "${compose[@]}" "$service"
  done
  "${compose[@]}" exec -T api /usr/local/bin/api --healthcheck
}

verify_release() {
  local manifest="$1" certificate_identity="$2" release count name image digest reference raw actual
  command -v jq >/dev/null || fail "jq is required"
  command -v cosign >/dev/null || fail "cosign is required"
  command -v gh >/dev/null || fail "GitHub CLI is required"
  release="$(jq -er '.release' "$manifest")"
  count="$(jq -er '.images | length' "$manifest")"
  test "$count" = "5" || fail "release manifest must contain five images"
  for name in "${image_names[@]}"; do
    image="$(jq -er --arg name "$name" '.images[$name].reference | split("@")[0]' "$manifest")"
    digest="$(jq -er --arg name "$name" '.images[$name].digest' "$manifest")"
    reference="$image@$digest"
    raw="$(mktemp "${TMPDIR:-/tmp}/mailflow-manifest.XXXXXX")"
    docker buildx imagetools inspect "$reference" --raw > "$raw"
    jq -e 'any(.manifests[]; .platform.os == "linux" and .platform.architecture == "amd64")' "$raw" >/dev/null
    jq -e 'any(.manifests[]; .platform.os == "linux" and .platform.architecture == "arm64")' "$raw" >/dev/null
    rm -f "$raw"
    actual="$(docker buildx imagetools inspect "$image:${release#v}" --raw | sha256sum | cut -d' ' -f1)"
    test "sha256:$actual" = "$digest" || fail "$name release tag does not resolve to its recorded digest"
    cosign verify "$reference" \
      --certificate-identity "$certificate_identity" \
      --certificate-oidc-issuer https://token.actions.githubusercontent.com >/dev/null
    gh attestation verify "oci://$reference" --repo "$GITHUB_REPOSITORY" >/dev/null
  done
}

case "${1:-}" in
  inspect) inspect_local_images ;;
  health) verify_stack_health ;;
  release)
    test "$#" = 3 || fail "usage: $0 release MANIFEST CERTIFICATE_IDENTITY"
    verify_release "$2" "$3"
    ;;
  *) fail "usage: $0 [inspect|health|release MANIFEST CERTIFICATE_IDENTITY]" ;;
esac
