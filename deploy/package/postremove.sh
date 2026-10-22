#!/bin/sh
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

set -eu
if command -v systemctl >/dev/null 2>&1 && test -d /run/systemd/system; then
  systemctl daemon-reload
fi
# Preserve /etc/godhcp, the service account and /var/lib/godhcp for recovery.
