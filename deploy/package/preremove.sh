#!/bin/sh
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

set -eu
# Debian passes remove/deconfigure, RPM passes 0 for the final uninstall.
case "${1:-}" in
  remove|deconfigure|0)
    if command -v systemctl >/dev/null 2>&1 && test -d /run/systemd/system; then
      systemctl stop godhcp.service || true
      systemctl disable godhcp.service || true
    fi
    ;;
esac
