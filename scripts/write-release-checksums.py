#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Checksum every binary, archive, package and guide in one release."""
import hashlib
from pathlib import Path
import sys

directory=Path(sys.argv[1])
files=sorted(p for p in directory.iterdir() if p.is_file() and (
    p.name.startswith(f'godhcp_{directory.name}_') or p.name.startswith(f'godhcp-{directory.name}-') or
    p.name in {'godhcp-admin-guide.pdf','godhcp-web-guide.pdf'}))
with (directory/'SHA256SUMS').open('w') as output:
    for path in files:
        output.write(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n')
print(directory.resolve())
