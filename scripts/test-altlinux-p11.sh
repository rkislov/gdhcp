#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
version=$(awk -F '"' '/Version = / {print $2}' internal/version/version.go)
result="$PWD/dist/altlinux-p11/$version"
ca="$PWD/output/altlinux-p11/$version/ca-certificates.crt"
pairs=${GODHCP_ALT_P11_TEST_ARCHES:-'x86_64:amd64 aarch64:arm64 i586:386'}
for pair in $pairs; do
  arch=${pair%:*}; platform=${pair#*:}
  echo "Testing ALT p11 $arch / linux/$platform"
  docker run --rm --platform "linux/$platform" \
    --mount "type=bind,src=$result,dst=/packages" \
    --mount "type=bind,src=$ca,dst=/ca.pem,readonly" \
    -e "GODHCP_VERSION=$version" -e "PACKAGE_ARCH=$arch" alt:p11 sh -ec '
      mkdir -p /etc/ssl/certs /etc/pki/tls/certs
      cp /ca.pem /etc/ssl/certs/ca-certificates.crt
      cp /ca.pem /etc/pki/tls/certs/ca-bundle.crt
      echo "Acquire::https::CaInfo \"/ca.pem\";" > /etc/apt/apt.conf.d/99godhcp-ca.conf
      sed -i "s|http://ftp.altlinux.org/pub/distributions/ALTLinux|https://download.basealt.ru/pub/distributions/ALTLinux|g" /etc/apt/sources.list.d/*.list
      apt-get -o Acquire::https::Timeout=30 update
      pkg="/packages/godhcp-$GODHCP_VERSION-alt1.p11.$PACKAGE_ARCH.rpm"
      apt-get install -y "$pkg" systemd-analyze
      test "$(rpm -qp --qf "%{ARCH}" "$pkg")" = "$PACKAGE_ARCH"
      /usr/bin/godhcp version | grep -F "godhcp $GODHCP_VERSION"
      /usr/libexec/godhcp/godhcp-fence-peer --help >/dev/null
      test -x /usr/libexec/godhcp/godhcp-cluster-ip
      test -s /usr/share/godhcp/fencing/GodhcpFencing.psm1
      test -s /usr/share/doc/godhcp/godhcp-admin-guide.pdf
      test -s /usr/share/doc/godhcp/godhcp-alt-p11-guide.pdf
      test -s /usr/share/doc/godhcp/examples/cluster/cluster.yml.example
      systemd-analyze verify /usr/lib/systemd/system/godhcp.service
      rpm -V godhcp
      cp /usr/bin/godhcp "/packages/verified-$PACKAGE_ARCH"
      printf "# retained configuration\n" > /etc/godhcp/config.yml
      printf "retained lease state\n" > /var/lib/godhcp/p11-test-state
      rpm -Uvh --replacepkgs "$pkg"
      rpm -e godhcp
      grep -q "retained configuration" /etc/godhcp/config.yml
      grep -q "retained lease state" /var/lib/godhcp/p11-test-state
      test ! -e /usr/bin/godhcp
      echo "ALT_P11_INSTALL_REINSTALL_REMOVE_OK $PACKAGE_ARCH"
    '
done
