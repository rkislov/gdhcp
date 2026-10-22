#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
load_arch=$(docker info --format '{{.Architecture}}')
case "$load_arch" in aarch64|arm64) load_arch=arm64 ;; x86_64|amd64) load_arch=amd64 ;; *) exit 1 ;; esac
load_context=$(mktemp -d)
trap 'rm -rf "$load_context"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH="$load_arch" go build -tags loadtest -trimpath -o "$load_context/godhcp" ./cmd/godhcp
CGO_ENABLED=0 GOOS=linux GOARCH="$load_arch" go build -tags loadtest -trimpath -o "$load_context/godhcp-load" tests/load/client.go
cp tests/load/lab.py "$load_context/lab.py"
cat > "$load_context/Dockerfile" <<'DOCKER'
FROM debian:12-slim
RUN apt-get update && apt-get install -y --no-install-recommends iproute2 python3 ca-certificates && rm -rf /var/lib/apt/lists/*
COPY godhcp godhcp-load /usr/local/bin/
COPY lab.py /lab.py
ENTRYPOINT ["python3", "-u", "/lab.py"]
DOCKER
docker build -t godhcp-load-lab:local "$load_context"
load_results=${GODHCP_LOAD_RESULTS:-$PWD/output/load-lab}
mkdir -p "$load_results"
docker run --rm --privileged --network none --cpus 4 --memory 2g \
  --mount "type=bind,src=$load_results,dst=/results" godhcp-load-lab:local "$@"
