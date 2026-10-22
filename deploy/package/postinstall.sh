#!/bin/sh
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

set -eu
if ! getent group godhcp >/dev/null; then
  groupadd --system godhcp
fi
if ! id godhcp >/dev/null 2>&1; then
  useradd --system --gid godhcp --home-dir /var/lib/godhcp --shell /usr/sbin/nologin godhcp
fi
install -d -m 0750 -o godhcp -g godhcp /etc/godhcp /var/lib/godhcp
if command -v systemctl >/dev/null 2>&1 && test -d /run/systemd/system; then
  systemctl daemon-reload
fi
echo 'GoDHCP installed. Configure /etc/godhcp/config.yml and godhcp.env before starting godhcp.service.'
