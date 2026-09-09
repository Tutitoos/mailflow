#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <Mailflow.app> <Mailflow.dmg> <updater.tar.gz> <version> <team-id>" >&2
  exit 2
fi

app_path=$1
dmg_path=$2
updater_path=$3
expected_version=$4
expected_team_id=$5
bundle_identifier=dev.tutitoos.mailflow
executable_name=mailflow-desktop

[[ -d "$app_path" && -f "$dmg_path" && -f "$updater_path" && "$expected_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]
[[ "$expected_team_id" =~ ^[A-Z0-9]{10}$ ]]

scratch_root=$(mktemp -d "${TMPDIR:-/tmp}/mailflow-release-verify.XXXXXX")
mount_point="$scratch_root/mount"
installed_root="$scratch_root/Applications"
mounted=false
app_pid=""

cleanup() {
  if [[ -n "$app_pid" ]] && kill -0 "$app_pid" 2>/dev/null; then
    kill "$app_pid" || true
    wait "$app_pid" || true
  fi
  if [[ "$mounted" == true ]]; then
    hdiutil detach "$mount_point" -quiet || true
  fi
  case "$scratch_root" in
    "${TMPDIR:-/tmp}"/mailflow-release-verify.*) rm -rf -- "$scratch_root" ;;
    *) echo "refusing to clean unexpected verification path" >&2 ;;
  esac
}
trap cleanup EXIT

verify_app() {
  local candidate=$1
  local executable="$candidate/Contents/MacOS/$executable_name"
  [[ -x "$executable" ]]
  [[ "$(plutil -extract CFBundleIdentifier raw "$candidate/Contents/Info.plist")" == "$bundle_identifier" ]]
  [[ "$(plutil -extract CFBundleShortVersionString raw "$candidate/Contents/Info.plist")" == "$expected_version" ]]

  local architectures
  architectures=$(lipo -archs "$executable")
  [[ " $architectures " == *" arm64 "* ]]
  [[ " $architectures " == *" x86_64 "* ]]

  codesign --verify --deep --strict --verbose=2 "$candidate"
  local signature
  signature=$(codesign --display --verbose=4 "$candidate" 2>&1)
  grep -Fq "Authority=Developer ID Application:" <<<"$signature"
  grep -Fq "TeamIdentifier=$expected_team_id" <<<"$signature"
  grep -Fq "Runtime Version=" <<<"$signature"

  local entitlements="$scratch_root/entitlements.plist"
  codesign --display --entitlements :- "$candidate" >"$entitlements" 2>/dev/null
  if /usr/libexec/PlistBuddy -c "Print :com.apple.security.get-task-allow" "$entitlements" 2>/dev/null | grep -iq '^true$'; then
    echo "release enables com.apple.security.get-task-allow" >&2
    return 1
  fi

  if otool -l "$executable" | grep -Eq '/Users/|/private/var/folders/'; then
    echo "release binary contains a private build rpath" >&2
    return 1
  fi
  spctl --assess --type execute --verbose=2 "$candidate"
  xcrun stapler validate "$candidate"
}

verify_app "$app_path"
hdiutil verify "$dmg_path"
spctl --assess --type open --context context:primary-signature --verbose=2 "$dmg_path"
xcrun stapler validate "$dmg_path"

mkdir -p "$mount_point" "$installed_root"
hdiutil attach "$dmg_path" -readonly -nobrowse -mountpoint "$mount_point" -quiet
mounted=true
mounted_app="$mount_point/Mailflow.app"
[[ -d "$mounted_app" ]]
[[ -L "$mount_point/Applications" ]]
verify_app "$mounted_app"

updater_root="$scratch_root/updater"
mkdir -p "$updater_root"
tar -tzf "$updater_path" | while IFS= read -r entry; do
  case "$entry" in
    /*|../*|*/../*|*/..) echo "updater archive contains an unsafe path" >&2; exit 1 ;;
  esac
done
tar -xzf "$updater_path" -C "$updater_root"
updater_app=$(find "$updater_root" -type d -name Mailflow.app -print -quit)
[[ -n "$updater_app" ]]
verify_app "$updater_app"

installed_app="$installed_root/Mailflow.app"
ditto "$mounted_app" "$installed_app"
hdiutil detach "$mount_point" -quiet
mounted=false
xattr -w com.apple.quarantine "0081;$(date +%s);MailflowReleaseVerification;" "$installed_app"
verify_app "$installed_app"

"$installed_app/Contents/MacOS/$executable_name" >/dev/null 2>&1 &
app_pid=$!
sleep 5
kill -0 "$app_pid"
kill "$app_pid"
wait "$app_pid" || true
app_pid=""

printf 'macOS release verification passed for Mailflow %s\n' "$expected_version"
