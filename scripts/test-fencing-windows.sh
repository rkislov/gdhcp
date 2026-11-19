#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
if command -v pwsh >/dev/null 2>&1; then
  pwsh -NoLogo -NoProfile -NonInteractive -File "$root/tests/fencing/test_windows.ps1"
else
  docker run --rm --platform linux/amd64 --network none -v "$root:/src:ro" \
    mcr.microsoft.com/powershell:7.4-debian-12 \
    pwsh -NoLogo -NoProfile -NonInteractive -File /src/tests/fencing/test_windows.ps1
fi
