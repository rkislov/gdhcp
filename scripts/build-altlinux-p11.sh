#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
source_tag=${PACKAGE_TAG:-v$version}
source_commit=$(git rev-parse "$source_tag^{}")
packaging_commit=$(git rev-parse HEAD)
result="$PWD/dist/altlinux-p11/$version"
stage="$PWD/output/altlinux-p11/$version"
mkdir -p "$result" "$stage"
# A real clone is intentional: this Go toolchain omits VCS metadata in worktrees.
if ! test -d "$stage/source/.git"; then
  git clone --quiet --shared --no-checkout "$PWD" "$stage/source"
fi
git -C "$stage/source" checkout --quiet --detach "$source_commit"
test -z "$(git -C "$stage/source" status --porcelain)"
source_version=$(awk -F '"' '/Version = / {print $2}' "$stage/source/internal/version/version.go")
test "$source_version" = "$version" || { echo "Tag/package version mismatch" >&2; exit 1; }
if test -f /etc/ssl/cert.pem; then cp /etc/ssl/cert.pem "$stage/ca-certificates.crt"; else cp /etc/ssl/certs/ca-certificates.crt "$stage/ca-certificates.crt"; fi
cp deploy/altlinux/Dockerfile.p11-builder "$stage/Dockerfile"
docker build -t godhcp-alt-p11-builder:local "$stage"
# Three public p11 repo architectures; optional preview ports are explicit.
architectures=${GODHCP_ALT_P11_ARCHES:-'x86_64 aarch64 i586 loongarch64 riscv64'}
for arch in $architectures; do
  case "$arch" in
    x86_64) goarch=amd64;; aarch64) goarch=arm64;; i586) goarch=386;;
    loongarch64) goarch=loong64;; riscv64) goarch=riscv64;;
    *) echo "Unsupported p11 architecture: $arch (e2k needs a separate Go port)" >&2; exit 1;;
  esac
  payload="$stage/payload-$arch"
  mkdir -p "$payload/usr/bin" "$payload/usr/lib/systemd/system" "$payload/etc/logrotate.d" \
    "$payload/etc/godhcp" "$payload/usr/share/doc/godhcp/examples" \
    "$payload/usr/libexec/godhcp" "$payload/usr/share/godhcp/fencing"
  port_flags=()
  if test "$goarch" = loong64 || test "$goarch" = riscv64; then
    python3 scripts/prepare-altlinux-ports.py "$stage/source" "$PWD/deploy/altlinux/ports" "$stage"
    port_flags=(-mod=mod -modfile "$stage/portable.mod")
    cp "$stage/PORTABILITY.json" "$result/PORTABILITY.json"
  fi
  (cd "$stage/source"; CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GO386=softfloat \
    go build "${port_flags[@]}" -trimpath -ldflags '-s -w' -o "$payload/usr/bin/godhcp" ./cmd/godhcp)
  go version -m "$payload/usr/bin/godhcp" > "$result/BUILDINFO-$arch.txt"
  rg -q "vcs.revision=$source_commit" "$result/BUILDINFO-$arch.txt"
  rg -q 'vcs.modified=false' "$result/BUILDINFO-$arch.txt"
  printf '%s\n' "$packaging_commit" > "$result/PACKAGING_COMMIT"
  cp "$payload/usr/bin/godhcp" "$result/godhcp_${version}_linux_$goarch"
  cp deploy/systemd/godhcp.service "$payload/usr/lib/systemd/system/"
  cp deploy/logrotate/godhcp "$payload/etc/logrotate.d/godhcp"
  cp deploy/package/config.yml.example "$payload/etc/godhcp/"
  cp -R docs/. "$payload/usr/share/doc/godhcp/"
  cp LICENSE NOTICE THIRD_PARTY_LICENSES "$payload/usr/share/doc/godhcp/"
  cp -R third_party "$payload/usr/share/doc/godhcp/"
  cp -R deploy/altlinux/ports "$payload/usr/share/doc/godhcp/examples/"
  cp -R deploy/cluster deploy/fencing "$payload/usr/share/doc/godhcp/examples/"
  cp scripts/godhcp-fence-peer scripts/godhcp-cluster-ip "$payload/usr/libexec/godhcp/"
  cp scripts/godhcp-fence-windows.ps1 scripts/GodhcpFencing.psm1 "$payload/usr/share/godhcp/fencing/"
  COPYFILE_DISABLE=1 tar --no-xattrs -czf "$stage/payload-$arch.tar.gz" -C "$payload" .
  docker run --rm --network none --mount "type=bind,src=$stage,dst=/input,readonly" \
    --mount "type=bind,src=$PWD/deploy/altlinux,dst=/spec,readonly" \
    --mount "type=bind,src=$result,dst=/result" \
    -e "PACKAGE_ARCH=$arch" -e "PACKAGE_VERSION=$version" godhcp-alt-p11-builder:local sh -ec '
      # ALT legacy rpmbuild uses flat paths; RPM ships target macros in platform/.
      # Link both legacy spellings to the official target macros, without editing them.
      test -f "/usr/lib/rpm/platform/$PACKAGE_ARCH-linux/macros"
      for suffix in linux alt-linux alt-linux-gnu; do
        dir="/usr/lib/rpm/$PACKAGE_ARCH-$suffix"
        test -e "$dir" || ln -s "platform/$PACKAGE_ARCH-linux" "$dir"
      done
      mkdir -p /root/RPM/SOURCES
      cp "/input/payload-$PACKAGE_ARCH.tar.gz" /root/RPM/SOURCES/godhcp-payload.tar.gz
      rpmbuild -bb --target "$PACKAGE_ARCH" \
        --define "godhcp_version $PACKAGE_VERSION" --define "_allow_root_build 1" \
        --define "_topdir /root/RPM" --define "__os_install_post %nil" /spec/godhcp-p11.spec
      cp /root/RPM/RPMS/"$PACKAGE_ARCH"/*.rpm /result/
    '
done
python3 scripts/write-release-checksums.py "$result"
