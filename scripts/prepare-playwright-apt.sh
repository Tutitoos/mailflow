#!/usr/bin/env bash
set -euo pipefail

readonly apt_root="${1:-/etc/apt}"
readonly sources_dir="$apt_root/sources.list.d"

[[ -d "$sources_dir" ]] || exit 0

move_source() {
  local source="$1" target="$source.mailflow-disabled"
  if [[ -e "$target" ]]; then
    echo "refusing to overwrite $target" >&2
    return 1
  fi
  if [[ -w "$sources_dir" ]]; then
    mv -- "$source" "$target"
  else
    sudo mv -- "$source" "$target"
  fi
}

shopt -s nullglob
for source in "$sources_dir"/*; do
  [[ -f "$source" ]] || continue
  [[ "$source" == *.mailflow-disabled ]] && continue
  if grep -Eq 'https?://dl\.google\.com/linux/chrome' "$source"; then
    echo "Disabling unrelated Google Chrome APT source: $source"
    move_source "$source"
  fi
done
