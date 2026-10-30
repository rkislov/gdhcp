#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
package_version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
docker run --rm --platform linux/amd64 \
 --mount "type=bind,src=$PWD/dist/altlinux-p10/$package_version,dst=/packages" \
 --mount "type=bind,src=$PWD/output/altlinux-p10/$package_version/ca-certificates.crt,dst=/ca.pem,readonly" --env GODHCP_VERSION="$package_version" alt:p10 sh -ec '
 mkdir -p /etc/ssl/certs /etc/pki/tls/certs
 cp /ca.pem /etc/ssl/certs/ca-certificates.crt
 cp /ca.pem /etc/pki/tls/certs/ca-bundle.crt
 echo "Acquire::https::CaInfo \"/ca.pem\";" > /etc/apt/apt.conf.d/99godhcp-ca.conf
 sed -i "s|http://ftp.altlinux.org/pub/distributions/ALTLinux|https://download.basealt.ru/pub/distributions/ALTLinux|g" /etc/apt/sources.list.d/*.list
 apt-get update >/dev/null
 apt-get install -y /packages/godhcp-$GODHCP_VERSION-alt1.p10.x86_64.rpm >/dev/null
 /usr/bin/godhcp version | grep -F "godhcp $GODHCP_VERSION"
 cp /usr/bin/godhcp /packages/extracted-godhcp
 id godhcp
 getent passwd godhcp | grep /sbin/nologin
 test -s /usr/share/doc/godhcp/godhcp-web-guide.pdf
 test -s /usr/share/doc/godhcp/godhcp-admin-guide.pdf
 cp /usr/share/doc/godhcp/godhcp-web-guide.pdf /packages/extracted-web.pdf
 cp /usr/share/doc/godhcp/godhcp-admin-guide.pdf /packages/extracted-admin.pdf
 rpm -V godhcp
 touch /var/lib/godhcp/preserve-test
 rpm -e godhcp
 test -f /var/lib/godhcp/preserve-test
 test ! -e /usr/bin/godhcp
 echo INSTALL_REMOVE_PRESERVATION_OK
 '
