#!/bin/bash
set -euo pipefail

version=$1
assets=$2
revision=$3
root=$(cd "$(dirname "$0")/.." && pwd)
release_tool="$root/scripts/release_desktop.py"
prerelease=$(python3 "$release_tool" validate "$version")
python3 "$release_tool" verify-assets "$version" "$revision" "$assets"

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

check_tag() {
  local remote_revision
  remote_revision=$(gh api "repos/$GH_REPO/commits/refs%2Ftags%2F$version" --jq .sha)
  if [ "$remote_revision" != "$revision" ]; then
    echo "Tag $version now resolves to $remote_revision, expected $revision" >&2
    exit 1
  fi
}

verify_upload() {
  gh release view "$version" --json assets --jq .assets > "$stage/assets.json"
  python3 "$release_tool" verify-assets "$version" "$revision" "$assets" --remote "$stage/assets.json"
}

check_tag
source_notes="$stage/source.md"
notes="$stage/notes.md"
base="https://github.com/$GH_REPO/releases/download/$version"
cat > "$source_notes" <<EOF
## Corresponding source

- [macOS Apple Silicon]($base/oie_${version}_darwin_arm64_source.tar.gz)
- [Linux x86_64]($base/oie_${version}_linux_amd64_source.tar.gz)

[SHA256SUMS]($base/SHA256SUMS) · [Build and verification receipt]($base/packaging-receipt.json)

EOF
cp "$source_notes" "$notes"
note_flags=(--notes-file "$notes")
if [ -f "$root/docs/releases/$version.md" ]; then
  cat "$root/docs/releases/$version.md" >> "$notes"
else
  note_flags+=(--generate-notes)
fi
flags=()
if [ "$prerelease" = true ]; then
  flags+=(--latest=false)
fi

if release=$(gh release view "$version" --json isDraft,url,body 2>"$stage/lookup-error"); then
  if [ "$(jq -r .isDraft <<< "$release")" = false ]; then
    verify_upload
    jq -r .url <<< "$release"
    exit 0
  fi
  jq -r .body <<< "$release" > "$stage/draft-notes.md"
  if ! grep -Fq "$base/oie_${version}_darwin_arm64_source.tar.gz" "$stage/draft-notes.md" ||
     ! grep -Fq "$base/oie_${version}_linux_amd64_source.tar.gz" "$stage/draft-notes.md"; then
    cat "$source_notes" "$stage/draft-notes.md" > "$notes"
    gh release edit "$version" --notes-file "$notes"
  fi
else
  if ! grep -Fxq 'release not found' "$stage/lookup-error"; then
    cat "$stage/lookup-error" >&2
    exit 1
  fi
  gh release create "$version" --verify-tag --draft --title "Oiê $version" "--prerelease=$prerelease" "${flags[@]}" "${note_flags[@]}"
fi

# Retrying a draft may replace assets. The public release is checked above.
gh release upload "$version" "$assets"/* --clobber
verify_upload
check_tag
gh release edit "$version" --draft=false "--prerelease=$prerelease" "${flags[@]}"
