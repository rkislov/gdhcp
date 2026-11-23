#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
source_commit=$(git rev-parse HEAD)
result="$PWD/dist/distro/$version"
mkdir -p "$result"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
nfpm_bin=${NFPM:-$HOME/go/bin/nfpm}
for arch in amd64 arm64; do
  binary="$PWD/dist/releases/$version/godhcp_${version}_linux_$arch"
  go version -m "$binary" > "$stage/buildinfo"
  rg -q "vcs.revision=$source_commit" "$stage/buildinfo"
  rg -q 'vcs.modified=false' "$stage/buildinfo"
  python3 - "$arch" "$version" "$binary" "$stage" <<'PY'
from pathlib import Path
import sys
arch,version,binary,stage=sys.argv[1:]
text=Path('deploy/nfpm.yaml').read_text()
for key,value in {'PACKAGE_ARCH':arch,'PACKAGE_VERSION':version,'PACKAGE_BINARY':binary}.items():
    text=text.replace('${'+key+'}',value)
for name,release in [('ubuntu','1ubuntu'),('el9','1.el9')]:
    Path(stage,name+'.yaml').write_text(text.replace('release: "1"','release: "'+release+'"'))
PY
  "$nfpm_bin" package --config "$stage/ubuntu.yaml" --packager deb --target "$result/godhcp_${version}-1ubuntu_${arch}.deb"
  rpm_arch=x86_64
  if test "$arch" = arm64; then rpm_arch=aarch64; fi
  "$nfpm_bin" package --config "$stage/el9.yaml" --packager rpm --target "$result/godhcp-${version}-1.el9.${rpm_arch}.rpm"
done
python3 scripts/write-release-checksums.py "$result"
