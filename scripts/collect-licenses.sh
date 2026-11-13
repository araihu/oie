#!/bin/sh
# Collect notices for the application and the Go packages linked by a target.
set -eu

source_dir=$1
packaging_dir=$2
destination=$3
go_license=$4
tags=$5
command=$6

mkdir -p "$destination/go"
cp -R "$packaging_dir/licenses/." "$destination/"
test -s "$destination/javascript/@babel/helpers/LICENSE"
test -s "$source_dir/internal/adapters/web/brand/NOTICE-ARAIHU.txt"
cp "$go_license" "$destination/go/LICENSE"

find "$source_dir/internal/adapters/web" "$source_dir/internal/adapters/desktop/assets" -type f |
  while IFS= read -r file; do
    relative=${file#"$source_dir/"}
    case "${file##*/}:$relative" in
      LICENSE*:*|LICENCE*:*|COPYING*:*|NOTICE*:*|Apache-2.0.txt:*|GPL-3.0.txt:*|Noto-upstream-OFL.txt:*|*:internal/adapters/web/appicons/LICENSES/*)
        mkdir -p "$destination/$(dirname "$relative")"
        cp "$file" "$destination/$relative" ;;
    esac
  done

module_paths=$(cd "$source_dir" && go list -mod=readonly -tags "$tags" -deps \
  -f '{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Dir}}{{end}}{{end}}' "$command")
tab=$(printf '\t')
printf '%s\n' "$module_paths" | LC_ALL=C sort -u |
  while IFS="$tab" read -r module_path module_dir; do
    [ -n "$module_path" ] && [ -n "$module_dir" ] || continue
    module_destination="$destination/go-modules/$module_path"
    for file in "$module_dir"/LICENSE* "$module_dir"/LICENCE* "$module_dir"/COPYING* \
      "$module_dir"/NOTICE* "$module_dir"/PATENTS*; do
      [ -f "$file" ] || continue
      mkdir -p "$module_destination"
      cp "$file" "$module_destination/"
    done
    case "$module_path" in
      github.com/araihu/goshtoso)
        for file in "$module_dir"/assets/icons/HEROICONS_LICENSE.txt "$module_dir"/assets/fonts/README.txt \
          "$module_dir"/assets/js/runtime/*/*/LICENSE.txt "$module_dir"/assets/licenses/*/*/LICENSE.txt; do
          relative=${file#"$module_dir/"}
          mkdir -p "$module_destination/$(dirname "$relative")"
          cp "$file" "$module_destination/$relative"
        done ;;
    esac
  done
