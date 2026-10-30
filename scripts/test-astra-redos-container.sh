#!/bin/sh
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -eu
format=$1
package=$2
case "$format" in
 deb)
  printf "path-include /usr/share/doc/godhcp\npath-include /usr/share/doc/godhcp/*\n" > /etc/dpkg/dpkg.cfg.d/zz-godhcp-docs
  apt-get -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 update
  DEBIAN_FRONTEND=noninteractive apt-get install -y "$package"
  dpkg-deb -f "$package" Package Version Architecture
  ;;
 rpm)
  dnf install -y "$package"
  rpm -qp --qf '%{NAME} %{VERSION}-%{RELEASE} %{ARCH}\n' "$package"
  rpm -V godhcp
  ;;
esac
godhcp version | grep -F "godhcp ${GODHCP_VERSION:?set expected package version}"
getent passwd godhcp
test -s /usr/share/doc/godhcp/godhcp-web-guide.pdf
systemd-analyze verify /usr/lib/systemd/system/godhcp.service
cp /usr/bin/godhcp "/packages/verified-binary-$TARGET_NAME"
printf '# retained configuration\n' > /etc/godhcp/config.yml
printf 'retained lease state\n' > /var/lib/godhcp/package-test-state
case "$format" in
 deb) dpkg -i "$package"; dpkg --remove godhcp ;;
 rpm) rpm -Uvh --replacepkgs "$package"; rpm -e godhcp ;;
esac
grep -q 'retained configuration' /etc/godhcp/config.yml
grep -q 'retained lease state' /var/lib/godhcp/package-test-state
test ! -e /usr/bin/godhcp
echo PACKAGE_INSTALL_REINSTALL_REMOVE_OK
