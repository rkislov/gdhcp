#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
context=$(mktemp -d)
container_id=
cleanup() {
 if test -n "$container_id"; then docker rm -f "$container_id" >/dev/null; fi
 rm -rf "$context"
}
trap cleanup EXIT
mkdir -p "$context/tests" "$context/scripts"
cp -R tests/active-backup "$context/tests/"
cp scripts/godhcp-cluster-ip "$context/scripts/"
if test -n "${GODHCP_TEST_BINARY:-}"; then
 cp "$GODHCP_TEST_BINARY" "$context/godhcp"
else
 CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$context/godhcp" ./cmd/godhcp
fi
# Reuse the distro/kernel build recipes, never a container PID 1 as a VM.
if ! docker image inspect godhcp-systemd-debian:local >/dev/null 2>&1; then
 cp -R tests/systemd tests/integration "$context/tests/"
 version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
 cp "dist/releases/$version/godhcp_${version}_linux_arm64.deb" "$context/package.deb"
 docker build --platform linux/arm64 -t godhcp-systemd-debian:local -f tests/systemd/debian.Dockerfile "$context"
fi
if ! docker image inspect godhcp-systemd-runner:local >/dev/null 2>&1; then
 docker build --platform linux/arm64 -t godhcp-systemd-runner:local -f tests/systemd/runner.Dockerfile "$context"
fi
docker build --platform linux/arm64 -t godhcp-pair-guest:local -f tests/active-backup/guest.Dockerfile "$context"
docker build --platform linux/arm64 -t godhcp-pair-runner:local -f tests/active-backup/runner.Dockerfile "$context"
container_id=$(docker create --platform linux/arm64 godhcp-pair-guest:local)
docker export "$container_id" -o "$context/rootfs.tar"
docker rm "$container_id" >/dev/null
container_id=
results=${GODHCP_PAIR_RESULTS:-$PWD/output/active-backup-vm}
mkdir -p "$results"
docker run --rm --privileged --network none --platform linux/arm64 \
 -e "GODHCP_PAIR_SOAK_SECONDS=${GODHCP_PAIR_SOAK_SECONDS:-0}" \
 --mount "type=bind,src=$context,dst=/input,readonly" \
 --mount "type=bind,src=$PWD,dst=/source,readonly" \
 --mount "type=bind,src=$results,dst=/results" \
 godhcp-pair-runner:local python3 -u /source/tests/active-backup/run-vm.py
