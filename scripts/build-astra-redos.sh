#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
default_version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
package_tag=${PACKAGE_TAG:-v$default_version}
package_version=${package_tag#v}
package_commit=$(git rev-parse "$package_tag^{}")
result="$PWD/dist/astra-redos/$package_version"
mkdir -p "$result" "$PWD/output/astra-redos"
stage=$(mktemp -d "$PWD/output/astra-redos/stage.XXXXXX")
git archive "$package_tag" | tar -xf - -C "$stage"
binary="$PWD/dist/releases/$package_version/godhcp_${package_version}_linux_amd64"
go version -m "$binary" > "$stage/binary-buildinfo.txt"
rg -q "vcs.revision=$package_commit" "$stage/binary-buildinfo.txt"
rg -q 'vcs.modified=false' "$stage/binary-buildinfo.txt"
cp "$binary" "$stage/godhcp"
python3 - "$stage" "$package_version" <<'PY'
from pathlib import Path
import sys
stage=Path(sys.argv[1]);version=sys.argv[2]
text=(stage/'deploy/nfpm.yaml').read_text()
# Only tracked documentation is shipped, including the illustrated PDF.
for key,value in {'PACKAGE_ARCH':'amd64','PACKAGE_VERSION':version,'PACKAGE_BINARY':'godhcp'}.items():text=text.replace('${'+key+'}',value)
for distro,release in [('astra','1astra'),('redos','1.redos')]:
 (stage/f'nfpm-{distro}.yaml').write_text(text.replace('release: "1"',f'release: "{release}"'))
PY
nfpm_bin=${NFPM:-$HOME/go/bin/nfpm}
(cd "$stage"; "$nfpm_bin" package --config nfpm-astra.yaml --packager deb --target "$result/godhcp_${package_version}-1astra_amd64.deb")
(cd "$stage"; "$nfpm_bin" package --config nfpm-redos.yaml --packager rpm --target "$result/godhcp-${package_version}-1.redos.x86_64.rpm")
cp "$stage"/nfpm-*.yaml "$stage/binary-buildinfo.txt" "$result/"
printf '%s\n' "$package_commit" > "$result/SOURCE_COMMIT"
python3 - "$result" <<'PY'
from pathlib import Path
import hashlib,sys
root=Path(sys.argv[1]);files=sorted(p for p in root.iterdir() if p.suffix in ['.deb','.rpm'])
(root/'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in files))
PY
