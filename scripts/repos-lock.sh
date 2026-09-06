#!/usr/bin/env bash

set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
lock_file="$workspace_root/deploy/repos.lock"
action=${1:-verify}

usage() {
  printf '%s\n' 'Usage: scripts/repos-lock.sh [validate|verify|clone-missing]'
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

case "$action" in
  validate|verify|clone-missing) ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

test -f "$lock_file" || die "lock file not found: $lock_file"
command -v git >/dev/null 2>&1 || die 'git is required'

paths=()
entries=0
line_number=0

while IFS= read -r line || test -n "$line"; do
  line_number=$((line_number + 1))
  test -n "$line" || continue
  case "$line" in \#*) continue ;; esac

  IFS=$'\t' read -r relative_path remote commit extra <<EOF
$line
EOF

  test -n "${relative_path:-}" || die "missing path at line $line_number"
  test -n "${remote:-}" || die "missing remote at line $line_number"
  test -n "${commit:-}" || die "missing commit at line $line_number"
  test -z "${extra:-}" || die "too many fields at line $line_number"

  case "$relative_path" in
    external/*) ;;
    *) die "path must be below external/ at line $line_number" ;;
  esac
  case "$relative_path" in
    *..*|*//*|*/.|*/../*) die "unsafe path at line $line_number: $relative_path" ;;
  esac
  case "$remote" in
    git@github.com:*/*.git) ;;
    *) die "remote must be a GitHub SSH URL at line $line_number" ;;
  esac
  case "$commit" in
    *[!0-9a-f]*|'') die "commit must be lowercase hexadecimal at line $line_number" ;;
  esac
  test "${#commit}" -eq 40 || die "commit must contain 40 characters at line $line_number"

  for seen_path in "${paths[@]-}"; do
    test "$seen_path" != "$relative_path" || die "duplicate path at line $line_number: $relative_path"
  done
  paths+=("$relative_path")
  entries=$((entries + 1))

  test "$action" != validate || continue
  target="$workspace_root/$relative_path"

  if test ! -d "$target/.git"; then
    if test "$action" = verify; then
      die "missing repository: $relative_path"
    fi
    test ! -e "$target" || die "$relative_path exists but is not an independent Git repository"
    mkdir -p "$(dirname "$target")"
    git clone --filter=blob:none --no-checkout "$remote" "$target"
    git -C "$target" fetch origin "$commit"
    git -C "$target" checkout --detach "$commit"
  fi

  actual_remote=$(git -C "$target" remote get-url origin)
  actual_commit=$(git -C "$target" rev-parse HEAD)
  test "$actual_remote" = "$remote" || die "remote mismatch: $relative_path"
  test "$actual_commit" = "$commit" || die "commit mismatch: $relative_path (have $actual_commit, want $commit)"
  test -z "$(git -C "$target" status --porcelain)" || die "dirty repository: $relative_path"
done < "$lock_file"

printf 'repos.lock %s passed (%d external repositories).\n' "$action" "$entries"
