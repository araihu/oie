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
COPY scripts/collect-licenses.sh scripts/
COPY packaging/licenses/ packaging/licenses/

ARG TARGETOS
ARG TARGETARCH
ENV GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build -mod=readonly -tags=server -trimpath -buildvcs=false -ldflags="-s -w" -o /out/oie ./cmd/oie

ARG OIE_RELEASE_VERSION
ARG OIE_RELEASE_REVISION
ARG OIE_SOURCE_URL
ARG OIE_SOURCE_SHA256
RUN set -eu; \
    install -d -m 0700 /runtime/data; install -d -m 1777 /runtime/tmp; \
    sh scripts/collect-licenses.sh /src /src/packaging /licenses "$(go env GOROOT)/LICENSE" server ./cmd/oie; \
    cp LICENSE THIRD_PARTY_NOTICES.md /licenses/; \
    test "$(dpkg-query -W -f='${Version}' ca-certificates)" = '20250419~deb12u2'; \
    mkdir -p /licenses/ca-certificates; \
    cp /usr/share/doc/ca-certificates/copyright /licenses/ca-certificates/; \
    if [ -n "$OIE_RELEASE_VERSION" ]; then \
        test -n "$OIE_RELEASE_REVISION" && test -n "$OIE_SOURCE_URL" && test -n "$OIE_SOURCE_SHA256"; \
        printf '%s\n' "Oiê $OIE_RELEASE_VERSION" "Source revision: $OIE_RELEASE_REVISION" \
          "Corresponding source: $OIE_SOURCE_URL" "Source SHA-256: $OIE_SOURCE_SHA256" \
          "Target: $TARGETOS/$TARGETARCH" > /licenses/SOURCE.txt; \
    fi

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
