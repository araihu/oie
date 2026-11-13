#!/bin/sh
set -eu

image=${1:?usage: sh scripts/smoke-image.sh IMAGE}
volume=$(docker volume create)
container=
cleanup() {
    result=$?
    if [ -n "$container" ]; then
        if [ "$result" -ne 0 ]; then
            docker logs "$container" >&2 || true
        fi
        docker rm -f "$container" >/dev/null || true
    fi
    docker volume rm "$volume" >/dev/null || true
}
trap cleanup EXIT

start() {
    container=$(docker run -d --publish 127.0.0.1::8080 \
        --mount "type=volume,source=$volume,target=/data" "$image")
    url="http://$(docker port "$container" 8080/tcp)"
    curl --fail --silent --show-error --retry 20 --retry-connrefused \
        --retry-delay 1 --retry-max-time 30 --max-time 5 "$url/api/v1/health" |
        jq -e '.status == "ready"' >/dev/null
}

identity() {
    curl --fail --silent --show-error --max-time 5 "$url/api/v1/engine" |
        jq -er '.id | select(type == "string" and length > 0)'
}

start
html=$(curl --fail --silent --show-error --max-time 5 --location "$url/")
printf '%s\n' "$html" | grep -Fq '<title>Oiê</title>'
engine_id=$(identity)
docker stop --time 10 "$container" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
docker rm "$container" >/dev/null
container=
start
test "$(identity)" = "$engine_id"
