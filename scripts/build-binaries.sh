#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
test -n "$version"
release_dir="dist/releases/$version"
mkdir -p "$release_dir"
for target in linux/amd64 linux/386 linux/armv7 linux/arm64 darwin/amd64 darwin/arm64; do
  target_os=${target%/*}
  target_arch=${target#*/}
  go_arch=$target_arch
  if test "$target_arch" = armv7; then go_arch=arm; fi
  binary="godhcp_${version}_${target_os}_${target_arch}"
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$go_arch" GOARM=7 go build -trimpath -ldflags '-s -w' \
    -o "$release_dir/$binary" ./cmd/godhcp
  stage_dir=$(mktemp -d)
  trap 'rm -rf "$stage_dir"' EXIT
  cp "$release_dir/$binary" "$stage_dir/godhcp"
  cp LICENSE NOTICE THIRD_PARTY_LICENSES README.md "$stage_dir/"
  cp -R third_party "$stage_dir/"
  mkdir -p "$stage_dir/configs"
  cp configs/example.yml configs/dev.yml "$stage_dir/configs/"
  tar -czf "$release_dir/$binary.tar.gz" -C "$stage_dir" .
  rm -rf "$stage_dir"
  trap - EXIT
done
python3 - "$release_dir" "$version" <<'PY'
import hashlib, pathlib, sys
directory, version = pathlib.Path(sys.argv[1]), sys.argv[2]
with (directory / 'SHA256SUMS').open('w') as output:
    targets = ('linux_amd64', 'linux_386', 'linux_armv7', 'linux_arm64', 'darwin_amd64', 'darwin_arm64')
    for target in targets:
        for suffix in ('', '.tar.gz'):
            path = directory / f'godhcp_{version}_{target}{suffix}'
            output.write(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n')
print(directory.resolve())
PY
