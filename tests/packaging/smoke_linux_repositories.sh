#!/bin/bash
# Wait for Pages to serve the deployed signed indexes and installer configs.
set -euo pipefail
url=${1%/}
expected=$2
scratch=$(mktemp -d)
trap 'rm -r "$scratch"' EXIT
cd "$scratch"

deadline=$((SECONDS + 660))
until curl -fsSL --max-time 20 "$url/linux-repositories.sha256" -o linux-repositories.sha256 &&
  printf '%s  %s\n' "$expected" linux-repositories.sha256 | sha256sum --check --status; do
  if (( SECONDS >= deadline )); then
    echo "The public Linux repository checksums do not match the deployed indexes at $url" >&2
    exit 1
  fi
  sleep 15
done

while read -r hash path; do
  mkdir -p "$(dirname "$path")"
  until curl -fsSL --max-time 20 "$url/$path" -o "$path" &&
    printf '%s  %s\n' "$hash" "$path" | sha256sum --check --status; do
    if (( SECONDS >= deadline )); then
      echo "The public Linux repository file $path does not match the deployed file at $url" >&2
      exit 1
    fi
    sleep 15
  done
done < linux-repositories.sha256
sha256sum --check linux-repositories.sha256
