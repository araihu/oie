#!/bin/sh
# Install a verified GitHub release without changing system dependencies or data.
set -eu
umask 077

version=v0.0.1-rc.0
prefix=
temporary=
stage=
lock=
changed=
previous=
complete=false

usage() {
  cat <<'EOF'
Usage: install.sh [--version VERSION] [--prefix DIR] [--help]

Install Oie from araihu/oie GitHub Releases. Default version: v0.0.1-rc.0.
Linux defaults to ~/.local. macOS defaults to ~/Applications and installs Oie.app
inside that directory. Quit Oie before installing or updating. Existing app data
is preserved. Dependencies are checked, never installed. No sudo is used.
EOF
}
fail() { printf 'Oie installer: %s\n' "$*" >&2; exit 1; }
target_for() {
  case "$1" in
    app) target="$prefix/Oie.app" ;;
    bin) target="$prefix/bin/oie-desktop" ;;
    icon) target="$prefix/share/icons/hicolor/512x512/apps/org.araihu.oie.png" ;;
    desktop) target="$prefix/share/applications/org.araihu.oie.desktop" ;;
    docs) target="$prefix/share/doc/oie" ;;
  esac
}
cleanup() {
  result=$?
  trap - EXIT HUP INT TERM
  if [ "$complete" = false ] && [ -n "$stage" ]; then
    for key in $changed; do
      target_for "$key"
      # If the backup rename never happened, the existing target is still the
      # original. An interruption between intent and rename must leave it alone.
      if [ ! -e "$stage/old-$key" ] && [ ! -L "$stage/old-$key" ]; then
        case " $previous " in *" $key "*) continue ;; esac
      fi
      if ! rm -rf "$target"; then
        printf 'Rollback could not replace %s. Backups remain in %s\n' "$target" "$stage" >&2
        result=1
        stage=
        break
      fi
      if [ -e "$stage/old-$key" ] || [ -L "$stage/old-$key" ]; then
        if ! mv "$stage/old-$key" "$target"; then
          printf 'Rollback failed. Recover %s from %s\n' "$target" "$stage/old-$key" >&2
          result=1
          # Retain every remaining backup for manual recovery.
          stage=
          break
        fi
      fi
    done
  fi
  [ -z "$stage" ] || rm -rf "$stage"
  [ -z "$temporary" ] || rm -rf "$temporary"
  [ -z "$lock" ] || rmdir "$lock" 2>/dev/null || true
  exit "$result"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

while [ "$#" -gt 0 ]; do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --version|--prefix)
      option=$1
      [ "$#" -ge 2 ] && [ -n "$2" ] || fail "$option requires a value"
      case "$2" in --*) fail "$option requires a value" ;; esac
      if [ "$option" = --version ]; then version=$2; else prefix=$2; fi
      shift 2 ;;
    *) fail "unknown argument: $1" ;;
  esac
done
case "$version" in v[0-9]*) ;; *) fail 'version must start with v followed by a number' ;; esac
case "$version" in *[!A-Za-z0-9._-]*) fail 'invalid version' ;; esac
system=$(uname -s)
machine=$(uname -m)
case "$system" in
  Linux)
    case "$machine" in x86_64|amd64) platform=linux_amd64 ;; *) fail "unsupported Linux architecture: $machine" ;; esac
    [ -n "$prefix" ] || prefix="${HOME:?HOME must be set}/.local"
    ;;
  Darwin)
    # uname reports x86_64 inside Rosetta; the hardware query identifies ARM Macs.
    if [ "$machine" = x86_64 ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then machine=arm64; fi
    case "$machine" in arm64|aarch64) platform=darwin_arm64 ;; *) fail "unsupported macOS architecture: $machine" ;; esac
    [ -n "$prefix" ] || prefix="${HOME:?HOME must be set}/Applications"
    ;;
  *) fail "unsupported operating system: $system" ;;
esac
if printf '%s' "$prefix" | LC_ALL=C grep '[[:cntrl:]]' >/dev/null; then fail 'prefix contains control characters'; fi
case "$prefix" in /*) ;; *) prefix="$PWD/$prefix" ;; esac
for command in curl tar gzip awk sed mktemp; do command -v "$command" >/dev/null 2>&1 || fail "required command not found: $command"; done
if command -v sha256sum >/dev/null 2>&1; then checksum=sha256sum
elif command -v shasum >/dev/null 2>&1; then checksum=shasum
else fail 'sha256sum or shasum is required'; fi

mkdir -p "$prefix"
prefix=$(CDPATH= cd -P -- "$prefix" && pwd)
if ! mkdir "$prefix/.oie-install.lock" 2>/dev/null; then fail "another installation owns $prefix/.oie-install.lock"; fi
lock="$prefix/.oie-install.lock"
target_for app
if [ "$system" = Linux ]; then running_target="$prefix/bin/oie-desktop"
else running_target="$prefix/Oie.app/Contents/MacOS/oie-desktop"; fi
if [ "$system" = Linux ] && [ -d /proc ]; then
  for process in /proc/[0-9]*/exe; do
    executable=$(readlink "$process" 2>/dev/null || true)
    if [ "$executable" = "$running_target" ] || [ "$executable" = "$running_target (deleted)" ]; then fail 'Quit Oie before updating this installation.'; fi
  done
elif command -v ps >/dev/null 2>&1; then
  if ps -axo comm= 2>/dev/null | awk -v target="$running_target" '$0 == target { found=1 } END { exit !found }'; then fail 'Quit Oie before updating this installation.'; fi
fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/oie-download.XXXXXX")
asset="oie_${version}_${platform}.tar.gz"
release="https://github.com/araihu/oie/releases/download/$version"
printf 'Downloading Oie %s for %s...\n' "$version" "$platform"
curl --fail --silent --show-error --location --connect-timeout 15 --max-time 300 --proto '=https' --proto-redir '=https' --output "$temporary/SHA256SUMS" "$release/SHA256SUMS"
curl --fail --silent --show-error --location --connect-timeout 15 --max-time 300 --proto '=https' --proto-redir '=https' --output "$temporary/$asset" "$release/$asset"
expected=$(awk -v asset="$asset" 'NF == 2 && ($2 == asset || $2 == "*" asset) { value=$1; count++ } END { if (count != 1 || length(value) != 64 || value ~ /[^0-9a-fA-F]/) exit 1; print tolower(value) }' "$temporary/SHA256SUMS") || fail 'release checksum is missing, invalid or duplicated'
if [ "$checksum" = sha256sum ]; then actual=$(sha256sum "$temporary/$asset" | awk '{print $1}')
else actual=$(shasum -a 256 "$temporary/$asset" | awk '{print $1}'); fi
[ "$actual" = "$expected" ] || fail 'release checksum does not match; nothing was installed'

# Reject traversal, links, devices and unexpected roots before extracting.
tar -tzf "$temporary/$asset" > "$temporary/members" || fail 'cannot read release archive'
tar -tvzf "$temporary/$asset" > "$temporary/types" || fail 'cannot inspect release archive'
awk '(substr($0,1,1) != "-" && substr($0,1,1) != "d") || / link to / || / -> / { exit 1 }' "$temporary/types" || fail 'archive contains links or special files'
awk -v platform="$platform" '
  { p=$0; sub(/^\.\//,"",p); sub(/\/$/,"",p); if (p == "." || p == "") next;
    if (p ~ /[^A-Za-z0-9._\/+@=-]/ || p ~ /^\//) exit 1;
    n=split(p,a,"/"); for(i=1;i<=n;i++) if(a[i]==".." || a[i]=="." || a[i]=="") exit 1;
    if(platform == "darwin_arm64") { if(p != "Oie.app" && index(p,"Oie.app/") != 1) exit 1; }
    else if(p != "bin" && p != "bin/oie-desktop" && p != "share" && p != "share/applications" && p != "share/applications/org.araihu.oie.desktop" && p != "share/icons" && p != "share/icons/hicolor" && p != "share/icons/hicolor/512x512" && p != "share/icons/hicolor/512x512/apps" && p != "share/icons/hicolor/512x512/apps/org.araihu.oie.png" && p != "share/doc" && p != "share/doc/oie" && index(p,"share/doc/oie/") != 1) exit 1;
  }' "$temporary/members" || fail 'archive contains an unsafe or unexpected path'
stage=$(mktemp -d "$prefix/.oie-install.XXXXXX")
mkdir "$stage/content"
tar -xzf "$temporary/$asset" -C "$stage/content" || fail 'cannot extract release archive'

replace() {
  key=$1
  source=$2
  target_for "$key"
  if [ -e "$target" ] || [ -L "$target" ]; then previous="$key $previous"; fi
  # Record rollback intent before either rename, including the signal window.
  changed="$key $changed"
  case " $previous " in *" $key "*) mv "$target" "$stage/old-$key" ;; esac
  mv "$source" "$target"
}
if [ "$system" = Darwin ]; then
  [ -x "$stage/content/Oie.app/Contents/MacOS/oie-desktop" ] && [ -f "$stage/content/Oie.app/Contents/Info.plist" ] || fail 'archive has no complete Oie.app'
  replace app "$stage/content/Oie.app"
  printf 'Installed %s/Oie.app\n' "$prefix"
  printf 'This alpha is not Developer ID signed or notarized. Use the documented macOS first-open procedure.\n'
else
  payload="$stage/content"
  [ -x "$payload/bin/oie-desktop" ] && [ -f "$payload/share/applications/org.araihu.oie.desktop" ] && [ -f "$payload/share/icons/hicolor/512x512/apps/org.araihu.oie.png" ] && [ -d "$payload/share/doc/oie" ] || fail 'archive is missing required application files'
  missing=false
  if command -v ldd >/dev/null 2>&1; then
    if ! ldd "$payload/bin/oie-desktop" > "$temporary/dependencies" 2>&1 || grep 'not found' "$temporary/dependencies" >/dev/null; then missing=true; cat "$temporary/dependencies" >&2; fi
  elif command -v ldconfig >/dev/null 2>&1; then
    if ! ldconfig -p > "$temporary/dependencies" 2>&1 || ! awk '$1 == "libgtk-4.so.1" { gtk=1 } $1 == "libwebkitgtk-6.0.so.4" { webkit=1 } $1 == "libsoup-3.0.so.0" { soup=1 } END { exit !(gtk && webkit && soup) }' "$temporary/dependencies"; then missing=true; fi
  else missing=true; fi
  if [ "$missing" = true ]; then
    printf 'Ubuntu runtime dependencies: sudo apt install libgtk-4-1 libwebkitgtk-6.0-4 libsoup-3.0-0 ca-certificates\n' >&2
    fail 'required native libraries are unavailable; install them and run the installer again'
  fi
  # Reject symlinked subdirectories instead of writing outside the chosen prefix.
  for directory in bin share share/applications share/icons share/icons/hicolor share/icons/hicolor/512x512 share/icons/hicolor/512x512/apps share/doc; do
    [ ! -L "$prefix/$directory" ] || fail "installation directory is a symlink: $prefix/$directory"
    mkdir -p "$prefix/$directory"
  done
  # Desktop Entry strings and Exec arguments have separate escaping rules.
  executable=$(printf '%s' "$prefix/bin/oie-desktop" | sed 's/\\/\\\\\\\\/g; s/"/\\\\"/g; s/[$`]/\\\\&/g; s/%/%%/g')
  icon=$(printf '%s' "$prefix/share/icons/hicolor/512x512/apps/org.araihu.oie.png" | sed 's/\\/\\\\/g')
  cat > "$stage/desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Oiê
Comment=Chat across your accounts
Comment[pt_BR]=Converse entre suas contas
Exec="$executable"
Icon=$icon
Terminal=false
Categories=Network;InstantMessaging;
StartupWMClass=org.araihu.oie
EOF
  replace bin "$payload/bin/oie-desktop"
  replace icon "$payload/share/icons/hicolor/512x512/apps/org.araihu.oie.png"
  replace desktop "$stage/desktop"
  replace docs "$payload/share/doc/oie"
  printf 'Installed %s/bin/oie-desktop\n' "$prefix"
  case ":${PATH:-}:" in *":$prefix/bin:"*) ;; *) printf 'Add %s/bin to your PATH to run oie-desktop from a terminal.\n' "$prefix" ;; esac
fi
complete=true
if ! command -v ffmpeg >/dev/null 2>&1; then printf 'FFmpeg is optional. Install it separately to generate local video thumbnails.\n'; fi
printf 'Installation complete. App data and preferences were preserved.\n'
