#!/bin/sh
set -eu

mailflow_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mailflow_postgres_id=""
mailflow_redis_id=""

mailflow_cleanup() {
  mailflow_status=$?
  trap - EXIT HUP INT TERM
  if [ "$mailflow_status" -ne 0 ]; then
    if [ -n "$mailflow_postgres_id" ]; then
      docker logs "$mailflow_postgres_id" 2>&1 || true
    fi
    if [ -n "$mailflow_redis_id" ]; then
      docker logs "$mailflow_redis_id" 2>&1 || true
    fi
  fi
  if [ -n "$mailflow_postgres_id" ]; then
    docker rm --force --volumes "$mailflow_postgres_id" >/dev/null 2>&1 || true
  fi
  if [ -n "$mailflow_redis_id" ]; then
    docker rm --force --volumes "$mailflow_redis_id" >/dev/null 2>&1 || true
  fi
  exit "$mailflow_status"
}
trap mailflow_cleanup EXIT
trap 'exit 130' HUP INT TERM

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required for Mailflow integration tests." >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "Docker is installed but its daemon is unavailable." >&2
  exit 1
fi

mailflow_postgres_id=$(docker run --detach \
  --env POSTGRES_DB=mailflow_test \
  --env POSTGRES_PASSWORD=mailflow_test \
  --env POSTGRES_USER=mailflow \
  --publish 127.0.0.1::5432 \
  postgres:18-alpine)
mailflow_redis_id=$(docker run --detach \
  --publish 127.0.0.1::6379 \
  redis:8-alpine)

mailflow_attempt=0
until docker exec "$mailflow_postgres_id" pg_isready --username mailflow --dbname mailflow_test >/dev/null 2>&1; do
  mailflow_attempt=$((mailflow_attempt + 1))
  if [ "$mailflow_attempt" -ge 60 ]; then
    echo "PostgreSQL 18 did not become ready within 60 seconds." >&2
    exit 1
  fi
  sleep 1
done

mailflow_attempt=0
until docker exec "$mailflow_redis_id" redis-cli ping >/dev/null 2>&1; do
  mailflow_attempt=$((mailflow_attempt + 1))
  if [ "$mailflow_attempt" -ge 60 ]; then
    echo "Redis 8 did not become ready within 60 seconds." >&2
    exit 1
  fi
  sleep 1
done

mailflow_postgres_port=$(docker port "$mailflow_postgres_id" 5432/tcp | sed -n 's/.*://p')
mailflow_redis_port=$(docker port "$mailflow_redis_id" 6379/tcp | sed -n 's/.*://p')
if [ -z "$mailflow_postgres_port" ] || [ -z "$mailflow_redis_port" ]; then
  echo "Docker did not publish the integration service ports." >&2
  exit 1
fi

export MAILFLOW_TEST_DATABASE_URL="postgres://mailflow:mailflow_test@127.0.0.1:${mailflow_postgres_port}/mailflow_test?sslmode=disable"
export MAILFLOW_TEST_REDIS_ADDRESS="127.0.0.1:${mailflow_redis_port}"

cd "$mailflow_root/services/api"
go test -race -count=1 -timeout=5m ./...
