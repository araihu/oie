#!/bin/bash
# Exercise apt-secure and the indexed download without altering the host APT config.
set -euo pipefail
scratch=$(mktemp -d)
trap 'rm -r "$scratch"' EXIT
mkdir -p "$scratch/lists/partial" "$scratch/download"
touch "$scratch/status"
expected_version=${4:-}
if [[ $1 == https://* ]]; then
  archive=${1%/}
  # Allow the previous Pages response to expire before using the public index.
  deadline=$((SECONDS + 660))
  until curl -fsSL --max-time 20 "$archive/dists/oie/InRelease" -o "$scratch/InRelease" &&
    printf '%s  %s\n' "$2" "$scratch/InRelease" | sha256sum --check --status; do
    if (( SECONDS >= deadline )); then
      echo "The public APT index does not match the deployed archive at $archive" >&2
      exit 1
    fi
    sleep 15
  done
  curl -fsSL --retry 3 --max-time 20 "$archive/oie.sources" -o "$scratch/oie.sources"
  printf '%s  %s\n' "$3" "$scratch/oie.sources" | sha256sum --check
else
  archive=$(realpath "$1/apt")
  sed "s|^URIs: .*|URIs: file:$archive|" "$archive/oie.sources" > "$scratch/oie.sources"
fi
options=(
  -o APT::Architecture=amd64
  -o Dir::Etc::sourcelist="$scratch/oie.sources"
  -o Dir::Etc::sourceparts=-
  -o Dir::State::lists="$scratch/lists"
  -o Dir::State::status="$scratch/status"
  -o Dir::Cache::pkgcache=""
  -o Dir::Cache::srcpkgcache=""
  -o Acquire::Languages=none
)
apt-get "${options[@]}" --error-on=any update
(cd "$scratch/download" && apt-get "${options[@]}" download oie)
for package in "$scratch/download"/*.deb; do
  [[ $(dpkg-deb -f "$package" Package) == oie ]]
  [[ $(dpkg-deb -f "$package" Architecture) == amd64 ]]
  if [[ -n $expected_version ]]; then
    actual_version=$(dpkg-deb -f "$package" Version)
    if [[ $actual_version != "$expected_version" ]]; then
      echo "Downloaded oie $actual_version, expected $expected_version" >&2
      exit 1
    fi
  fi
done
