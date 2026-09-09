#!/usr/bin/env bash
set -euo pipefail

readonly repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly test_root="$(mktemp -d "${TMPDIR:-/tmp}/mailflow-production-edge.XXXXXX")"
readonly project="mailflow_edge_acceptance_$$"
readonly digest='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
readonly traefik_image='traefik@sha256:16acb89c6db341182970d6fdafece31303b0a380a8ed7aa51682e225229bf1d2'
readonly dynamic_config="$test_root/traefik-dynamic.yml"
readonly override="$test_root/override.yml"

cleanup() {
  local status=$?
  if (( status != 0 )) && declare -p compose >/dev/null 2>&1; then
    "${compose[@]}" logs --no-color --tail 100 traefik >&2 || true
  fi
  docker compose --project-name "$project" --env-file "$repository_root/deploy/.env.production.example" \
    -f "$repository_root/deploy/compose.yml" -f "$repository_root/deploy/compose.production.yml" \
    -f "$override" down --volumes --remove-orphans >/dev/null 2>&1 || true
  find "$test_root" -type f -delete
  rmdir "$test_root" 2>/dev/null || true
  return "$status"
}
trap cleanup EXIT

chmod 700 "$test_root"
sed -e 's/__MAILFLOW_DOMAIN__/mail.example.com/g' -e 's/^      tls:$/      tls: {}/' \
  -e '/certResolver: letsencrypt/d' -e 's#http://web:8080#http://127.0.0.1:8082#' \
  "$repository_root/deploy/traefik-dynamic.yml.template" > "$dynamic_config"
chmod 600 "$dynamic_config"
printf '%s\n' \
  'services:' \
  '  traefik:' \
  "    image: $traefik_image" \
  '    ports: !override' \
  '      - target: 8080' \
  '        published: "0"' \
  '        host_ip: 127.0.0.1' \
  '      - target: 8443' \
  '        published: "0"' \
  '        host_ip: 127.0.0.1' \
  'networks:' \
  '  edge:' \
  "    name: ${project}_edge" > "$override"

export MAILFLOW_TRAEFIK_CONFIG_PATH="$dynamic_config"
export MAILFLOW_TRAEFIK_IMAGE="$traefik_image"
export MAILFLOW_WEB_IMAGE="ghcr.io/tutitoos/mailflow-web@sha256:$digest"
export MAILFLOW_AUTH_IMAGE="ghcr.io/tutitoos/mailflow-auth@sha256:$digest"
export MAILFLOW_API_IMAGE="ghcr.io/tutitoos/mailflow-api@sha256:$digest"
export MAILFLOW_WORKER_IMAGE="ghcr.io/tutitoos/mailflow-worker@sha256:$digest"
export MAILFLOW_BACKUP_IMAGE="ghcr.io/tutitoos/mailflow-backup@sha256:$digest"
export MAILFLOW_POSTGRES_IMAGE="postgres@sha256:$digest"
export MAILFLOW_REDIS_IMAGE="redis@sha256:$digest"

readonly -a compose=(
  docker compose --project-name "$project" --env-file "$repository_root/deploy/.env.production.example"
  -f "$repository_root/deploy/compose.yml" -f "$repository_root/deploy/compose.production.yml"
  -f "$override"
)
"${compose[@]}" up -d --no-deps --wait --wait-timeout 60 traefik

readonly container="$("${compose[@]}" ps -q traefik)"
readonly http_port="$("${compose[@]}" port traefik 8080 | awk -F: '{print $NF}')"
readonly https_port="$("${compose[@]}" port traefik 8443 | awk -F: '{print $NF}')"
[[ -n "$container" && -n "$http_port" && -n "$https_port" ]]
[[ "$(docker inspect "$container" --format '{{.State.Health.Status}}')" == "healthy" ]]
[[ "$(docker inspect "$container" --format '{{.HostConfig.ReadonlyRootfs}}')" == "true" ]]
docker inspect "$container" --format '{{json .HostConfig.CapDrop}}' | grep -q 'ALL'
! docker inspect "$container" --format '{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}' \
  | grep -Fx '/var/run/docker.sock'

readonly redirect_status="$(curl --silent --output /dev/null --write-out '%{http_code}' \
  -H 'Host: mail.example.com' "http://127.0.0.1:$http_port/")"
[[ "$redirect_status" == "301" || "$redirect_status" == "308" ]]
readonly routed_status="$(curl --insecure --silent --output /dev/null --write-out '%{http_code}' \
  --noproxy '*' --resolve "mail.example.com:$https_port:127.0.0.1" \
  "https://mail.example.com:$https_port/ping")"
[[ "$routed_status" == "200" ]]
readonly response_headers="$(curl --insecure --silent --head --noproxy '*' \
  --resolve "mail.example.com:$https_port:127.0.0.1" \
  "https://mail.example.com:$https_port/ping")"
grep -Eqi '^strict-transport-security: max-age=31536000' <<< "$response_headers"
grep -Eqi '^x-content-type-options: nosniff' <<< "$response_headers"

echo "production edge acceptance passed"
