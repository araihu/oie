# Oiê corresponding source

Each desktop binary in GitHub Releases points to a matching
`_source.tar.gz`. Its `SOURCE.txt` gives the download URL and the full
Git revision. Its `build-info.json` records the source archive's SHA-256.
The release lists both downloads and includes `SHA256SUMS` and a
`packaging-receipt.json` with the build and verification results. The tagged
OCI image carries the Linux source URL, SHA-256 and revision in
`/licenses/SOURCE.txt`. Publication of that image requires the public
source archive and an offline build report for both Linux architectures.

Verify the archive with `sha256sum --check --ignore-missing SHA256SUMS` on
Linux. On macOS, use `shasum -a 256 -c SHA256SUMS --ignore-missing`.
From inside the extracted `oie_<version>_source` directory, use the Go version
recorded in the release receipt and the native build prerequisites in
`oie/docs/installation.md`.

```sh
export GOENV=off GOTOOLCHAIN=local GOWORK=off GOPROXY=off GOSUMDB=off
mkdir -p "$PWD/bin"
CGO_ENABLED=1 go -C oie build -mod=vendor -tags production -trimpath -buildvcs=false \
  -o "$PWD/bin/" ./cmd/oie ./cmd/oie-desktop
CGO_ENABLED=1 go -C oie/cli build -mod=vendor -trimpath -buildvcs=false \
  -o "$PWD/bin/oiectl" ./cmd/oiectl
```

On macOS, the release targets macOS 13.0. To match that deployment target, set
`MACOSX_DEPLOYMENT_TARGET=13.0` and
`CGO_CFLAGS='-O2 -g -mmacosx-version-min=13.0'` and set `CGO_LDFLAGS`
to the same flags before building. The bundle recipe in
`release-tooling/scripts/package-desktop.sh` also sets the linker flag and
applies the ad-hoc signature.

Rebuild the OCI executable from the Linux source archive with the server
configuration. Set `GOARCH=arm64` for the arm64 image:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C oie build -mod=vendor -tags server \
  -trimpath -buildvcs=false -ldflags="-s -w" -o "$PWD/bin/oie-server" ./cmd/oie
```

- `oie/` contains the tagged application, templates, build and generation scripts,
  and pinned `vendor/` and `cli/vendor/` trees produced by `go mod vendor`.
- `go-sources/` contains the exact Go module archives used by the target and
  the `oapi-cli-gen` generator. These also supply generation inputs such as
  Wails TypeScript, Goshtoso's asset recipe and the modernc Go translations.
- `browser-sources/` contains the pinned upstream JavaScript, WebAssembly
  decoder, icon and artwork sources. It includes the Mermaid and PDF.js
  distributions with source maps. `release-tooling/packaging/browser-source.json`
  records the downloaded and packaged archive digests and the input manifests
  or assets they cover. Selected upstream tarballs are repacked as inner
  uncompressed tar archives with the editable inputs and notices, excluding
  unrelated test fixtures, demo assets and site media. For example, the PDF.js
  test corpus contains PDF works with separate terms and is omitted.
- `native-sources/` contains the SQLite 3.54.1 canonical tree and amalgamation,
  SQLite-vec, the musl revision pinned by modernc.org/libc, and the Debian
  source package for the OCI image's Mozilla CA bundle. The tagged Dockerfile
  records the builder image and the installed CA package version.
- The source verification fails when a pinned input or packaged archive
  changes. Update the affected source archives, input digests and notices
  together.
- `release-tooling/` contains the packaging recipe, installer, verification
  scripts and third-party license notices.

Older archives include a README with the build procedure for their version.
