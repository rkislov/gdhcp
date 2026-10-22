#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail
cd "$(dirname "$0")/.."
version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
context=$(mktemp -d)
container_id=
results_root=${GODHCP_SYSTEMD_RESULTS:-$PWD/output/systemd-lab}
cleanup() {
  if test -n "$container_id"; then docker rm "$container_id" >/dev/null; fi
  python3 - "$context" <<'PY'
import shutil,sys
shutil.rmtree(sys.argv[1])
PY
}
trap cleanup EXIT
mkdir -p "$context/tests"
cp -R tests/systemd tests/integration "$context/tests/"
cp "dist/releases/$version/godhcp_${version}_linux_arm64.deb" "$context/package.deb"
cp "dist/releases/$version/godhcp_${version}_linux_arm64.rpm" "$context/package.rpm"
docker build --platform linux/arm64 -t godhcp-systemd-runner:local -f tests/systemd/runner.Dockerfile "$context"
if test "$#" -eq 0; then set -- debian rocky; fi
for guest in "$@"; do
    case "$guest" in debian|rocky) ;; *) echo "Unknown guest: $guest" >&2; exit 1 ;; esac
    docker build --platform linux/arm64 -t "godhcp-systemd-$guest:local" -f "tests/systemd/$guest.Dockerfile" "$context"
    mkdir -p "$results_root/$guest"
    container_id=$(docker create --platform linux/arm64 "godhcp-systemd-$guest:local")
    docker export "$container_id" -o "$context/rootfs.tar"
    docker rm "$container_id" >/dev/null
    container_id=
    docker run --rm --network none --platform linux/arm64 \
      --mount "type=bind,src=$context,dst=/input,readonly" \
      --mount "type=bind,src=$PWD/tests/systemd,dst=/lab,readonly" \
      --mount "type=bind,src=$results_root/$guest,dst=/results" \
      godhcp-systemd-runner:local python3 -u /lab/run-vm.py
done
