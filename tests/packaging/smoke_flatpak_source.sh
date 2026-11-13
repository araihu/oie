#!/bin/bash
# Build the corresponding Oiê source with the Flatpak SDK and no network access.
set -euo pipefail
archive=$(realpath "$1")
toolchain=$(GOTOOLCHAIN=local go env GOROOT)
scratch=$(mktemp -d)
trap 'rm -r "$scratch"' EXIT

tar -xf "$archive" -C "$scratch" --strip-components=1 --wildcards '*/oie/*'
flatpak build-init --arch=x86_64 "$scratch/build" com.araihu.oie org.gnome.Sdk org.gnome.Platform 50
flatpak build --unshare=network \
  --bind-mount="/run/oie-source=$scratch/oie" \
  --bind-mount="/run/oie-go=$toolchain" "$scratch/build" sh -ec '
    export PATH=/run/oie-go/bin:$PATH GOENV=off GOTOOLCHAIN=local GOWORK=off
    export GOPROXY=off GOSUMDB=off CGO_ENABLED=1 GOCACHE=/var/tmp/oie-go-cache
    go version
    pkg-config --modversion gtk4 webkitgtk-6.0 libsoup-3.0 gio-unix-2.0
    mkdir -p /app/bin
    go -C /run/oie-source build -mod=vendor -tags production -trimpath -buildvcs=false \
      -o /app/bin/oie-desktop ./cmd/oie-desktop
  '

flatpak build --runtime --unshare=network "$scratch/build" sh -ec '
  ldd -r /app/bin/oie-desktop >/tmp/oie-linkage 2>&1
  if grep -E "not found|undefined symbol" /tmp/oie-linkage; then
    exit 1
  fi
  /app/bin/oie-desktop --help
'
