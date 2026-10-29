#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
if test -n "$(git status --porcelain)"; then
  echo "Commit source changes before building a release RPM." >&2
  exit 1
fi
package_version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
stage="$PWD/output/altlinux-p10/$package_version"
result="$PWD/dist/altlinux-p10/$package_version"
mkdir -p "$stage" "$result"
payload=$(mktemp -d "$stage/payload.XXXXXX")
mkdir -p "$payload/usr/bin" "$payload/usr/lib/systemd/system" "$payload/etc/godhcp" "$payload/usr/share/doc/godhcp" "$result"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o "$payload/usr/bin/godhcp" ./cmd/godhcp
cp deploy/systemd/godhcp.service "$payload/usr/lib/systemd/system/"
cp deploy/package/config.yml.example "$payload/etc/godhcp/"
cp -R docs/. "$payload/usr/share/doc/godhcp/"
cp LICENSE NOTICE THIRD_PARTY_LICENSES "$payload/usr/share/doc/godhcp/"
cp -R third_party "$payload/usr/share/doc/godhcp/"
if test -f /etc/ssl/cert.pem; then cp /etc/ssl/cert.pem "$stage/ca-certificates.crt"; else cp /etc/ssl/certs/ca-certificates.crt "$stage/ca-certificates.crt"; fi
COPYFILE_DISABLE=1 tar -czf "$stage/godhcp-payload.tar.gz" -C "$payload" .
docker run --rm --platform linux/amd64 \
 --mount "type=bind,src=$stage,dst=/input,readonly" \
 --mount "type=bind,src=$PWD/deploy/altlinux,dst=/spec,readonly" \
 --mount "type=bind,src=$result,dst=/result" --env GODHCP_VERSION="$package_version" alt:p10 sh -ec '
 sed -i "s|http://ftp.altlinux.org/pub/distributions/ALTLinux|https://download.basealt.ru/pub/distributions/ALTLinux|g" /etc/apt/sources.list.d/*.list
 echo "Acquire::https::CaInfo \"/input/ca-certificates.crt\";" > /etc/apt/apt.conf.d/99godhcp-ca.conf
 mkdir -p /etc/ssl/certs /etc/pki/tls/certs
 cp /input/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
 cp /input/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt
 apt-get -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 update
 apt-get install -y rpm-build >/dev/null
 mkdir -p /root/RPM/SOURCES
 cp /input/godhcp-payload.tar.gz /root/RPM/SOURCES/
 rpmbuild -bb --define "godhcp_version $GODHCP_VERSION" --define "_allow_root_build 1" --define "_topdir /root/RPM" --define "__os_install_post %nil" /spec/godhcp.spec
 cp /root/RPM/RPMS/x86_64/*.rpm /result/
 '
