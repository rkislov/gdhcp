#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""VM fixture: exercise production IP hook for both address families."""
import subprocess,sys
action=sys.argv[1]
for prefix in ('192.0.2.10/24','fd42:10::1/64'):
    subprocess.run(['/usr/libexec/godhcp/godhcp-cluster-ip',action,'eth0',prefix],check=True)
