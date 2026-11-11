#!/bin/sh
# Build the selected immutable source, not the checkout containing this script.
set -eu
# The selected source controls VCS metadata, even when a Git alias launches us.
unset GIT_DIR GIT_WORK_TREE GIT_PREFIX GIT_INDEX_FILE

fail() { printf '%s\n' "package-desktop: $*" >&2; exit 1; }
usage() {
  printf '%s\n' 'Usage: package-desktop.sh --version TAG --revision FULL_SHA --source DIR --output DIR'
}
copy_file() {
  # Fresh package resources need their contents, not the builder's extended
  # attributes. Shared build directories may not support macOS metadata.
  if [ "$target_os" = darwin ]; then cp -X "$@"; else cp "$@"; fi
}
version= revision= source_dir= output_dir=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version|--revision|--source|--output)
      option=$1
      [ "$#" -ge 2 ] || fail "missing value for $option"
      case "$option" in
        --version) version=$2 ;;
        --revision) revision=$2 ;;
        --source) source_dir=$2 ;;
        --output) output_dir=$2 ;;
      esac
      shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; fail "unknown argument: $1" ;;
  esac
done
[ -n "$version" ] && [ -n "$revision" ] && [ -n "$source_dir" ] && [ -n "$output_dir" ] || { usage >&2; exit 1; }
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$' || fail 'version must be a version tag such as v0.0.1-rc.0'
printf '%s\n' "$revision" | LC_ALL=C grep -Eq '^[0-9a-f]{40}$' || fail 'revision must be a full lowercase Git SHA'
[ -d "$source_dir" ] || fail "source directory does not exist: $source_dir"
source_dir=$(cd "$source_dir" && pwd -P)
mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd -P)
case "$output_dir/" in "$source_dir/"*) fail 'output must be outside the source directory' ;; esac
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
tooling_dir=$(CDPATH= cd -- "$script_dir/.." && pwd -P)
[ -f "$source_dir/go.mod" ] && [ -f "$source_dir/cmd/oie-desktop/main.go" ] || fail 'source does not contain the Oie desktop application'
[ -f "$source_dir/LICENSE" ] && [ -f "$source_dir/THIRD_PARTY_NOTICES.md" ] || fail 'source licensing files are missing'
for art_dir in internal/adapters/desktop/assets internal/adapters/web/brand; do
  [ -s "$source_dir/$art_dir/Apache-2.0.txt" ] || fail "source artwork license is missing: $art_dir/Apache-2.0.txt"
done

source_mode=archive-attested
build_vcs=false
if [ -e "$source_dir/.git" ]; then
  command -v git >/dev/null 2>&1 || fail 'Git is required to verify a Git source checkout'
  actual_revision=$(git -C "$source_dir" rev-parse HEAD)
  tag_revision=$(git -C "$source_dir" rev-parse --verify "refs/tags/$version^{commit}") || fail "source tag is unavailable: $version"
  [ "$actual_revision" = "$revision" ] && [ "$tag_revision" = "$revision" ] || fail 'source HEAD, tag and supplied revision differ'
  # Packaging/CI tools may come from a newer checkout. Every application input
  # in this selected source must remain exactly as tagged, including embeds.
  dirty=$(git -C "$source_dir" status --porcelain --untracked-files=all -- go.mod go.sum cmd internal api cli LICENSE THIRD_PARTY_NOTICES.md)
  [ -z "$dirty" ] || fail 'application source differs from its tag'
  source_mode=git-tag
  build_vcs=true
fi
# Archive sources deliberately have no .git. The caller must obtain the tree
# from the supplied revision, e.g. git archive TAG, and verify that provenance.

case "$(uname -s):$(uname -m)" in
  Darwin:arm64) target_os=darwin; target_arch=arm64 ;;
  Linux:x86_64) target_os=linux; target_arch=amd64 ;;
  *) fail 'build natively on macOS Apple Silicon or Ubuntu 24.04 x86_64' ;;
esac
command -v go >/dev/null 2>&1 || fail 'Go 1.27.3 or newer is required'
export GOFLAGS= GOWORK=off CGO_ENABLED=1 GOOS=$target_os GOARCH=$target_arch
host_os=$(go env GOHOSTOS)
host_arch=$(go env GOHOSTARCH)
[ "$host_os:$host_arch" = "$target_os:$target_arch" ] || fail 'the Go toolchain must run on the selected native target'
go_version=$(cd "$source_dir" && go env GOVERSION)
printf '%s\n' "$go_version" | LC_ALL=C awk '
  /^go[0-9]+\.[0-9]+\.[0-9]+$/ {
    sub(/^go/, ""); split($0, v, ".")
    if (v[1] > 1 || (v[1] == 1 && (v[2] > 27 || (v[2] == 27 && v[3] >= 3)))) ok=1
  }
  END { exit !ok }
' || fail "Go 1.27.3 or newer is required, found $go_version"

build_os= build_sdk= deployment_target=
if [ "$target_os" = darwin ]; then
  for tool in sips iconutil plutil codesign xcrun; do command -v "$tool" >/dev/null 2>&1 || fail "missing macOS build tool: $tool"; done
  deployment_target=13.0
  build_os=$(sw_vers -productVersion)
  build_sdk=$(xcrun --sdk macosx --show-sdk-version)
  for native_version in "$build_os" "$build_sdk"; do
    printf '%s\n' "$native_version" | LC_ALL=C grep -Eq '^[0-9]+(\.[0-9]+)*$' || fail 'unexpected macOS or SDK version'
  done
  export MACOSX_DEPLOYMENT_TARGET=13.0
  export CGO_CFLAGS='-O2 -g -mmacosx-version-min=13.0'
  export CGO_LDFLAGS='-O2 -g -mmacosx-version-min=13.0'
  link_flags='-s -w -extldflags=-mmacosx-version-min=13.0'
else
  [ -r /etc/os-release ] || fail 'Ubuntu release information is unavailable'
  ubuntu_id=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
  ubuntu_version=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | tr -d '"')
  [ "$ubuntu_id:$ubuntu_version" = ubuntu:24.04 ] || fail 'Linux release artifacts must be built on Ubuntu 24.04'
  command -v pkg-config >/dev/null 2>&1 || fail 'pkg-config is required'
  pkg-config --exists gtk4 webkitgtk-6.0 libsoup-3.0 gio-unix-2.0 || fail 'install libgtk-4-dev libwebkitgtk-6.0-dev libsoup-3.0-dev'
  build_os=ubuntu-24.04
  link_flags='-s -w'
fi

short_version=${version#v}
short_version=${short_version%%-*}
short_version=${short_version%%+*}
archive_name="oie_${version}_${target_os}_${target_arch}.tar.gz"
[ ! -e "$output_dir/$archive_name" ] || fail "archive already exists: $output_dir/$archive_name"
[ ! -e "$output_dir/$archive_name.sha256" ] || fail 'archive checksum already exists'
if [ "$target_os" = darwin ]; then
  # Signing and icon tools operate on native macOS storage. Only the finished
  # archive crosses into a possibly shared output filesystem.
  stage=$(mktemp -d "${TMPDIR:-/tmp}/oie-package.XXXXXX")
else
  stage=$(mktemp -d "$output_dir/.oie-package.XXXXXX")
fi
archive_tmp=
cleanup() {
  [ -z "$archive_tmp" ] || rm -f "$archive_tmp"
  rm -rf "$stage"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
payload="$stage/payload"
mkdir -p "$payload"
if [ "$target_os" = darwin ]; then
  bundle="$payload/Oie.app"
  executable="$bundle/Contents/MacOS/oie-desktop"
  resources="$bundle/Contents/Resources"
  doc_dir="$resources"
  mkdir -p "$(dirname "$executable")" "$resources"
  sed -e "s|@VERSION@|$version|g" -e "s|@SHORT_VERSION@|$short_version|g" -e "s|@SOURCE_REVISION@|$revision|g" \
    "$tooling_dir/packaging/macos/Info.plist.in" > "$bundle/Contents/Info.plist"
  plutil -lint "$bundle/Contents/Info.plist"
else
  executable="$payload/bin/oie-desktop"
  doc_dir="$payload/share/doc/oie"
  mkdir -p "$payload/bin" "$payload/share/applications" "$payload/share/icons/hicolor/512x512/apps" "$doc_dir"
  copy_file "$tooling_dir/packaging/linux/org.araihu.oie.desktop.in" "$payload/share/applications/org.araihu.oie.desktop"
  copy_file "$source_dir/internal/adapters/desktop/assets/icon.png" "$payload/share/icons/hicolor/512x512/apps/org.araihu.oie.png"
fi

printf '%s\n' "Building $version ($revision) for $target_os/$target_arch" >&2
(cd "$source_dir" && go build -mod=readonly -tags production -trimpath -buildvcs="$build_vcs" -ldflags "$link_flags" -o "$executable" ./cmd/oie-desktop)
chmod 0755 "$executable"
copy_file "$source_dir/LICENSE" "$source_dir/THIRD_PARTY_NOTICES.md" "$doc_dir/"
mkdir -p "$doc_dir/licenses"
# Retain the embedded browser/artwork notices in a browsable relative tree.
find "$source_dir/internal/adapters/web" "$source_dir/internal/adapters/desktop/assets" -type f | while IFS= read -r file; do
  case "$(basename "$file")" in
    LICENSE*|LICENCE*|COPYING*|NOTICE*|Apache-2.0.txt)
      relative=${file#"$source_dir/"}
      mkdir -p "$doc_dir/licenses/$(dirname "$relative")"
      copy_file "$file" "$doc_dir/licenses/$relative" ;;
  esac
done
go_root=$(cd "$source_dir" && go env GOROOT)
go_license="$go_root/LICENSE"
# Homebrew keeps the Go SDK under libexec but relocates its license to the
# versioned formula directory. Retain that exact SDK license in the bundle.
if [ ! -f "$go_license" ] && [ "$target_os" = darwin ] && [ "$(basename "$go_root")" = libexec ]; then
  go_license="$(dirname "$go_root")/LICENSE"
  [ -f "$go_license" ] && grep -q '^Copyright 2009 The Go Authors\.' "$go_license" || fail 'the Homebrew Go SDK license is missing or unrecognized'
fi
[ -f "$go_license" ] || fail "the Go SDK license is missing: $go_root/LICENSE"
mkdir -p "$doc_dir/licenses/go"
copy_file "$go_license" "$doc_dir/licenses/go/LICENSE"
(cd "$source_dir" && go list -mod=readonly -tags production -deps -f '{{if .Module}}{{.Module.Path}}{{"\t"}}{{.Module.Dir}}{{end}}' ./cmd/oie-desktop) > "$stage/modules-raw.tsv"
LC_ALL=C sort -u "$stage/modules-raw.tsv" > "$stage/modules.tsv"
tab=$(printf '\t')
while IFS="$tab" read -r module_path module_dir; do
  [ -n "$module_path" ] && [ -n "$module_dir" ] || continue
  for file in "$module_dir"/LICENSE* "$module_dir"/LICENCE* "$module_dir"/COPYING* "$module_dir"/NOTICE*; do
    [ -f "$file" ] || continue
    destination="$doc_dir/licenses/go-modules/$module_path"
    mkdir -p "$destination"
    copy_file "$file" "$destination/$(basename "$file")"
  done
done < "$stage/modules.tsv"
# Include the resolved dependency versions without leaking builder paths.
(cd "$source_dir" && go list -mod=readonly -m all) > "$doc_dir/go-modules.txt"
built_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
cat > "$doc_dir/build-info.json" <<JSON
{
  "version": "$version",
  "sourceRevision": "$revision",
  "sourceMode": "$source_mode",
  "buildVCS": $build_vcs,
  "goVersion": "$go_version",
  "os": "$target_os",
  "arch": "$target_arch",
  "buildOS": "$build_os",
  "buildSDK": "$build_sdk",
  "macOSDeploymentTarget": "$deployment_target",
  "builtAt": "$built_at"
}
JSON
cat > "$doc_dir/SOURCE.txt" <<SOURCE
Oiê $version
Source revision: $revision
Source: https://github.com/araihu/oie/tree/$revision
Source verification: $source_mode
Go: $go_version
Target: $target_os/$target_arch
SOURCE

if [ "$target_os" = darwin ]; then
  iconset="$stage/oie.iconset"
  mkdir -p "$iconset"
  for size in 16 32 128 256 512; do
    sips -z "$size" "$size" "$source_dir/internal/adapters/desktop/assets/icon.png" --out "$iconset/icon_${size}x${size}.png" >/dev/null
    double=$((size * 2))
    sips -z "$double" "$double" "$source_dir/internal/adapters/desktop/assets/icon.png" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
  done
  iconutil -c icns "$iconset" -o "$resources/oie.icns"
  codesign --force --deep --sign - --timestamp=none "$bundle"
  codesign --verify --deep --strict --verbose=2 "$bundle"
  plutil -lint "$bundle/Contents/Info.plist"
fi
if [ "$target_os" = darwin ]; then
  COPYFILE_DISABLE=1 tar -czf "$stage/$archive_name" -C "$payload" Oie.app
else
  tar -czf "$stage/$archive_name" -C "$payload" bin share
fi
archive_tmp=$(mktemp "$output_dir/.oie-archive.XXXXXX")
copy_file "$stage/$archive_name" "$archive_tmp"
chmod 0644 "$archive_tmp"
mv "$archive_tmp" "$output_dir/$archive_name"
archive_tmp=
(cd "$output_dir" && if command -v sha256sum >/dev/null 2>&1; then sha256sum "$archive_name"; else shasum -a 256 "$archive_name"; fi) > "$output_dir/$archive_name.sha256"
printf '%s\n' "$output_dir/$archive_name"
