#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
stage="$PWD/output/altlinux-p11/$version"
packages="$PWD/dist/altlinux-p11/$version"
image=${GODHCP_QEMU_IMAGE:-godhcp-p11-qemu-test:local}
if ! docker image inspect "$image" >/dev/null 2>&1; then
  docker build -t "$image" -f deploy/altlinux/Dockerfile.port-test deploy/altlinux
fi
for arch in loong64 riscv64; do
  qemu=$arch
  if test "$arch" = loong64; then qemu=loongarch64; fi
  (cd "$stage/source"; CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go test -mod=mod -modfile "$stage/portable.mod" -c -o "$stage/cluster-$arch.test" ./internal/cluster)
  docker run --rm --network none -w /work/source/internal/cluster -v "$stage:/work:ro" -v "$packages:/packages:ro" \
    -e "GODHCP_ARCH=$arch" -e "GODHCP_QEMU=$qemu" -e "GODHCP_VERSION=$version" \
    "$image" sh -ec '
      qemu-$GODHCP_QEMU /packages/godhcp_${GODHCP_VERSION}_linux_$GODHCP_ARCH version
      qemu-$GODHCP_QEMU /work/cluster-$GODHCP_ARCH.test -test.v -test.timeout=5m
    '
  echo "ALT_P11_QEMU_CLUSTER_OK $arch"
done
