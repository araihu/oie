#!/bin/bash
# Exercise apt-secure and the indexed download without altering the host APT config.
set -euo pipefail
archive=$(realpath "$1/apt")
scratch=$(mktemp -d)
trap 'rm -r "$scratch"' EXIT
mkdir -p "$scratch/lists/partial" "$scratch/download"
touch "$scratch/status"
sed "s|^URIs: .*|URIs: file:$archive|" "$archive/oie.sources" > "$scratch/oie.sources"
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
apt-get "${options[@]}" update
(cd "$scratch/download" && apt-get "${options[@]}" download oie)
for package in "$scratch/download"/*.deb; do
  [[ $(dpkg-deb -f "$package" Package) == oie ]]
  [[ $(dpkg-deb -f "$package" Architecture) == amd64 ]]
done
