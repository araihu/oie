#!/bin/sh
set -eu

# Order stable releases and numbered prereleases by application version.
tags=$(git -c versionsort.suffix=-alpha. -c versionsort.suffix=-beta. \
    -c versionsort.suffix=-rc. ls-remote --refs --tags --sort=-version:refname origin)
printf '%s\n' "$tags" |
    awk -F '\t' '$2 ~ /^refs\/tags\/v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$/ {
        sub(/^refs\/tags\//, "", $2)
        print $2
    }'
