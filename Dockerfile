# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/oie/ cmd/oie/
COPY internal/ internal/
COPY LICENSE THIRD_PARTY_NOTICES.md ./

ARG TARGETOS
ARG TARGETARCH
ENV GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build -mod=readonly -tags=server -trimpath -buildvcs=false -ldflags="-s -w" -o /out/oie ./cmd/oie

RUN set -eu; \
    install -d -m 0700 /runtime/data; install -d -m 1777 /runtime/tmp; \
    mkdir -p /licenses/go; cp LICENSE THIRD_PARTY_NOTICES.md /licenses/; \
    cp "$(go env GOROOT)/LICENSE" /licenses/go/; \
    find internal/adapters/web -type f \( -name 'LICENSE*' -o -name 'NOTICE*' -o \
        -name 'Apache-2.0.txt' -o -name 'GPL-3.0.txt' -o -name 'Noto-upstream-OFL.txt' \) \
        -exec cp --parents -t /licenses {} +; \
    go list -mod=readonly -tags=server -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Dir}}{{end}}{{end}}' ./cmd/oie > /tmp/oie-modules; \
    sort -u /tmp/oie-modules | while read -r module directory; do \
        [ -n "$module" ] || continue; \
        for notice in "$directory"/LICENSE* "$directory"/LICENCE* "$directory"/COPYING* "$directory"/NOTICE*; do \
            [ -f "$notice" ] || continue; \
            install -D -m 0644 "$notice" "/licenses/go-modules/$module/${notice##*/}"; \
        done; \
    done

FROM scratch
LABEL org.opencontainers.image.title="Oiê" \
      org.opencontainers.image.source="https://github.com/araihu/oie" \
      org.opencontainers.image.licenses="GPL-3.0-only"
COPY --from=build /out/oie /oie
COPY --from=build /licenses/ /licenses/
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /runtime/tmp/ /tmp/
COPY --from=build --chown=65532:65532 /runtime/data/ /data/
USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/oie"]
CMD ["--listen", "0.0.0.0:8080", "--data-dir", "/data"]
