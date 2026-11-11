#!/bin/sh
set -eu
if [ "$#" -eq 0 ]; then
  set -- env GOWORK=off go tool -modfile=tools/vacuum/go.mod vacuum
fi
python3 tools/generate-reaction-emoji-bases.py --check
tmp=$(mktemp -d "./.codegen-check.XXXXXX")
tmp=$(cd "$tmp" && pwd)
trap 'rm -rf "$tmp"' EXIT
# sqlc resolves paths relative to its configuration file.
root=$(pwd)
source_rel=..
cat > "$tmp/sqlc.yaml" <<EOF
version: '2'
sql:
  - engine: sqlite
    schema: "$source_rel/internal/adapters/sqlite/migrations"
    queries: "$source_rel/internal/adapters/sqlite/queries"
    gen:
      go:
        package: dbgen
        out: dbgen
        emit_empty_slices: true
        emit_interface: true
        emit_pointers_for_null_types: true
EOF
GOWORK=off go tool -modfile=tools/go.mod sqlc generate -f "$tmp/sqlc.yaml"
diff -ru internal/adapters/sqlite/dbgen "$tmp/dbgen"
sed "s|^output:.*|output: $tmp/api.gen.go|" api/codegen.yaml > "$tmp/codegen.yaml"
GOWORK=off go tool -modfile=tools/go.mod oapi-codegen --config "$tmp/codegen.yaml" api/openapi.yaml
cmp internal/adapters/httpapi/engineapi/api.gen.go "$tmp/api.gen.go"
sed "s|^output:.*|output: $tmp/preview.gen.go|" api/ui-preview.codegen.yaml > "$tmp/preview-codegen.yaml"
GOWORK=off go tool -modfile=tools/go.mod oapi-codegen --config "$tmp/preview-codegen.yaml" api/ui-preview.openapi.yaml
cmp internal/adapters/previewapi/api.gen.go "$tmp/preview.gen.go"
sed "s|^output:.*|output: $tmp/bff.gen.go|" api/bff.codegen.yaml > "$tmp/bff-codegen.yaml"
GOWORK=off go tool -modfile=tools/go.mod oapi-codegen --config "$tmp/bff-codegen.yaml" api/bff.openapi.yaml
cmp internal/adapters/web/bffapi/api.gen.go "$tmp/bff.gen.go"
sed "s|^output:.*|output: $tmp/cache.gen.go|" api/cache.codegen.yaml > "$tmp/cache-codegen.yaml"
GOWORK=off go tool -modfile=tools/go.mod oapi-codegen --config "$tmp/cache-codegen.yaml" api/cache.openapi.yaml
cmp internal/adapters/web/cacheapi/api.gen.go "$tmp/cache.gen.go"
GOWORK=off go tool -modfile=tools/go.mod templ generate -check
root=$(pwd)
"$@" bundle --composed "$root/api/openapi.yaml" "$tmp/bundle.yaml" > /dev/null
"$@" apply-overlay --fail-on-warnings "$tmp/bundle.yaml" "$root/api/cli.overlay.yaml" "$tmp/cli.yaml" > /dev/null
"$@" bundle --composed --format json "$tmp/cli.yaml" "$tmp/cli.json" > /dev/null
cmp api/openapi.cli.json "$tmp/cli.json"
GOWORK=off go tool -modfile=tools/go.mod oapi-cli-gen generate --spec api/openapi.cli.json --name oiectl --module github.com/araihu/oie/cli --out cli --check
