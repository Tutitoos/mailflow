#!/usr/bin/env bash
set -euo pipefail

readonly version="${VERSION:-0.0.0-local}"
[[ "$version" =~ ^0\.0\.0-(local|pr\.[0-9]+)$ ]] || {
  echo "acceptance image version must be 0.0.0-local or 0.0.0-pr.NUMBER" >&2
  exit 1
}

docker build --build-arg "VERSION=$version" --target web -t mailflow-web:acceptance -f apps/web/Dockerfile .
docker build --build-arg "VERSION=$version" --target auth -t mailflow-auth:acceptance -f services/auth/Dockerfile .
docker build --build-arg "VERSION=$version" --target api -t mailflow-api:acceptance -f services/api/Dockerfile .
docker build --build-arg "VERSION=$version" --target worker -t mailflow-worker:acceptance -f services/api/Dockerfile .
docker build --build-arg "VERSION=$version" --target backup -t mailflow-backup:acceptance -f services/api/backup.Dockerfile .
