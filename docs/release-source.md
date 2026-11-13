# Oiê corresponding source

The source archive for each desktop target contains the tagged Oiê tree, the
packaging recipe, the Go module archives used by that target and the browser
sources. The application tree includes the HTML templates and the scripts for
generating code and acquiring pinned assets. `release-tooling/packaging`
contains the additional third-party notices and the browser source checksums.

From the extracted archive, build the desktop command with Go 1.27.4 or newer
and the native libraries listed in `oie/docs/installation.md`.

```sh
export GOTOOLCHAIN=local GOWORK=off GOPROXY="file://$PWD/go-proxy" GOSUMDB=off
CGO_ENABLED=1 go -C oie build -mod=readonly -tags production -trimpath -buildvcs=false ./cmd/oie-desktop
go -C oie/cli build -mod=readonly ./cmd/oiectl
```

Verify the source archive against the release's `SHA256SUMS` before using
the bundled Go proxy offline. The builds still check the pinned `go.sum` files.

The exact build and installer scripts are under `release-tooling/scripts`.
The `browser-sources` directory contains editable upstream sources, and the
Mermaid and PDF.js npm archives in it also include the source maps that
identify the code and dependencies compiled into the shipped bundles. It also
includes the PDF.js WebAssembly decoder sources and their build scripts. The
Wails TypeScript source is in its Go module archive. The Arai Hû assets archive
contains the original Oiê artwork and generator, and Oiê's asset manifest
records the exact bytes used in the binaries.

Publish each target's `_source.tar.gz` beside its binary archive and list both
in the release notes and `SHA256SUMS`.
