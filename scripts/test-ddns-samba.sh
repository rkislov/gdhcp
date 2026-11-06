#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
chmod 0755 "$work"
network="godhcp-ddns-test-$$"
trap 'docker network rm "$network" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT
arch=$(docker info --format '{{.Architecture}}')
case "$arch" in aarch64|arm64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) echo "Unsupported Docker architecture: $arch" >&2; exit 1 ;; esac
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -tags=integration ./internal/ddns -o "$work/ddns-integration.test"
docker build -t godhcp-ddns-samba-test:local tests/integration/ddns
docker network create --internal "$network" >/dev/null
docker run --rm --network "$network" --hostname dc.dhcp.test -v "$work:/test:ro" godhcp-ddns-samba-test:local
