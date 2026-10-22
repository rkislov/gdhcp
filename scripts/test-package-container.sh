#!/bin/sh
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

# Run only inside a disposable Debian or Rocky Linux container.
set -eu
format=$1
package=$2
case "$format" in
  deb)
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$package"
    dpkg-deb --contents "$package" > /tmp/package-contents
    grep -q /usr/share/doc/godhcp/admin-guide.pdf /tmp/package-contents
    ;;
  rpm)
    dnf install -y "$package"
    test -f /usr/share/doc/godhcp/admin-guide.pdf
    ;;
esac
godhcp version
getent passwd godhcp
test ! -e /etc/godhcp/config.yml
systemd-analyze verify /usr/lib/systemd/system/godhcp.service
printf '# retained configuration\n' > /etc/godhcp/config.yml
printf 'retained state\n' > /var/lib/godhcp/test-state
case "$format" in
  deb) dpkg -i "$package" ;;
  rpm) rpm -Uvh --replacepkgs "$package" ;;
esac
grep -q 'retained configuration' /etc/godhcp/config.yml
case "$format" in
  deb) dpkg --remove godhcp ;;
  rpm) rpm -e godhcp ;;
esac
grep -q 'retained configuration' /etc/godhcp/config.yml
grep -q 'retained state' /var/lib/godhcp/test-state
echo 'PACKAGE_INSTALL_REINSTALL_REMOVE_OK'
