#!/bin/bash
set -euo pipefail

version=$1
assets=$2
revision=$3
root=$(cd "$(dirname "$0")/.." && pwd)
prerelease=$(python3 "$root/scripts/release_desktop.py" validate "$version")
(cd "$assets" && sha256sum --strict -c SHA256SUMS)

# Recheck that the tag still refers to the commit which built the assets.
remote_revision=$(gh api "repos/$GH_REPO/commits/$version" --jq .sha)
if [ "$remote_revision" != "$revision" ]; then
  echo "Tag $version now resolves to $remote_revision, expected $revision" >&2
  exit 1
fi

notes=(--generate-notes)
if [ -f "$root/docs/releases/$version.md" ]; then
  notes=(--notes-file "$root/docs/releases/$version.md")
fi
flags=()
if [ "$prerelease" = true ]; then
  flags+=(--latest=false)
fi

if release=$(gh release view "$version" --json isDraft,url); then
  if [ "$(jq -r .isDraft <<< "$release")" = false ]; then
    published=$(mktemp)
    trap 'rm -f "$published"' EXIT
    gh release download "$version" --pattern SHA256SUMS --output "$published" --clobber
    cmp "$assets/SHA256SUMS" "$published"
    jq -r .url <<< "$release"
    exit 0
  fi
else
  gh release create "$version" --verify-tag --draft --title "Oiê $version" "--prerelease=$prerelease" "${flags[@]}" "${notes[@]}"
fi

# Retrying a draft may replace these assets. Published releases are immutable.
gh release upload "$version" "$assets"/* --clobber
gh release edit "$version" --draft=false "--prerelease=$prerelease" "${flags[@]}"
