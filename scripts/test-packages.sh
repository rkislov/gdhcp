#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail
cd "$(dirname "$0")/.."
package_version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
for package_arch in amd64 arm64; do
  for format in deb rpm; do
    image=debian:12-slim
    if test "$format" = rpm; then image=rockylinux:9; fi
    echo "Testing $image / $package_arch / $format"
    docker run --rm --platform "linux/$package_arch" \
      -v "$PWD/dist/releases/$package_version:/packages:ro" \
      -v "$PWD/scripts/test-package-container.sh:/test.sh:ro" \
      "$image" sh /test.sh "$format" "/packages/godhcp_${package_version}_linux_${package_arch}.$format"
  done
done
