#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
case "$(docker info --format '{{.Architecture}}')" in
 aarch64|arm64) arch=arm64 ;;
 x86_64|amd64) arch=amd64 ;;
 *) echo "Unsupported Docker architecture" >&2; exit 1 ;;
esac
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$work/godhcp" ./cmd/godhcp
mkdir "$work/tests"
cp -R tests/integration "$work/tests/"
docker build -t godhcp-v6-lab:local -f tests/integration/Dockerfile "$work"
docker run --rm --privileged --network none --entrypoint bash godhcp-v6-lab:local /lab/dhcp6_lab.sh
