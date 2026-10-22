#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail
cd "$(dirname "$0")/.."
package_version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
test -n "$package_version"
test -f output/pdf/godhcp-admin-guide.pdf || {
  echo 'Generate output/pdf/godhcp-admin-guide.pdf first (see docs/building.md).' >&2
  exit 1
}
python3 scripts/check-licenses.py
release_dir="dist/releases/$package_version"
mkdir -p "$release_dir"
run_nfpm() {
  if test -n "${NFPM:-}"; then
    "$NFPM" "$@"
  else
    go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0 "$@"
  fi
}
for package_arch in amd64 arm64; do
  stage_dir=$(mktemp -d)
  trap 'rm -rf "$stage_dir"' EXIT
  CGO_ENABLED=0 GOOS=linux GOARCH="$package_arch" go build -trimpath -ldflags '-s -w' -o "$stage_dir/godhcp" ./cmd/godhcp
  cp "$stage_dir/godhcp" "$release_dir/godhcp_${package_version}_linux_${package_arch}"
  cp deploy/systemd/godhcp.service "$stage_dir/"
  cp deploy/package/config.yml.example "$stage_dir/"
  cp -R docs "$stage_dir/"
  cp output/pdf/godhcp-admin-guide.pdf "$stage_dir/"
  cp LICENSE NOTICE THIRD_PARTY_LICENSES "$stage_dir/"
  cp -R third_party "$stage_dir/"
  tar -czf "$release_dir/godhcp_${package_version}_linux_${package_arch}.tar.gz" -C "$stage_dir" .
  python3 - "$package_arch" "$package_version" "$stage_dir" <<'PY'
import pathlib, sys
arch, version, stage = sys.argv[1:]
config = pathlib.Path('deploy/nfpm.yaml').read_text()
for key, value in {'PACKAGE_ARCH': arch, 'PACKAGE_VERSION': version, 'PACKAGE_BINARY': stage + '/godhcp'}.items():
    config = config.replace('${' + key + '}', value)
pathlib.Path(stage, 'nfpm.yaml').write_text(config)
PY
  for format in deb rpm; do
      run_nfpm package --config "$stage_dir/nfpm.yaml" --packager "$format" \
        --target "$release_dir/godhcp_${package_version}_linux_${package_arch}.$format"
  done
  rm -rf "$stage_dir"
  trap - EXIT
done
python3 - "$release_dir" <<'PY'
import hashlib, pathlib, sys
directory = pathlib.Path(sys.argv[1])
files = sorted(p for p in directory.glob('godhcp_*_linux_*') if p.is_file())
with (directory / 'SHA256SUMS').open('w') as stream:
    for path in files:
        stream.write(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n')
print(directory.resolve())
PY
