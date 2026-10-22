#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail
cd "$(dirname "$0")/.."
command -v docker >/dev/null
arch=$(docker info --format '{{.Architecture}}')
case "$arch" in aarch64|arm64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) echo "Unsupported Docker architecture: $arch" >&2; exit 1 ;; esac
context=$(mktemp -d)
trap 'rm -rf "$context"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$context/godhcp" ./cmd/godhcp
mkdir -p "$context/tests"
cp -R tests/integration "$context/tests/"
docker build -t godhcp-network-lab:local -f tests/integration/Dockerfile "$context"
mkdir -p output/dhcp-lab
# All links and namespaces live inside this disposable container. No host DHCP ports.
docker run --rm --privileged --network none --mount "type=bind,src=$PWD/output/dhcp-lab,dst=/results" godhcp-network-lab:local
# Recreate the entire container, keeping only its mounted state directory.
docker run --rm --privileged --network none --mount "type=bind,src=$PWD/output/dhcp-lab,dst=/results" godhcp-network-lab:local --verify-recreated
