#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
test -n "$version"
release_dir="dist/releases/$version"
mkdir -p "$release_dir"
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags '-s -w' \
    -o "$release_dir/godhcp_${version}_linux_${arch}" ./cmd/godhcp
done
python3 - "$release_dir" "$version" <<'PY'
import hashlib, pathlib, sys
directory, version = pathlib.Path(sys.argv[1]), sys.argv[2]
with (directory / 'SHA256SUMS').open('w') as output:
    for path in sorted(directory.glob(f'godhcp_{version}_linux_*')):
        if path.is_file():
            output.write(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n')
print(directory.resolve())
PY
