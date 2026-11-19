#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Prepare isolated dependency copies for preview ports; never edit GOMODCACHE."""
import hashlib
import json
import pathlib
import shutil
import subprocess
import sys

source, ports, stage = map(lambda p: pathlib.Path(p).resolve(), sys.argv[1:])
modfile = stage / 'portable.mod'
shutil.copyfile(source / 'go.mod', modfile)
shutil.copyfile(source / 'go.sum', modfile.with_suffix('.sum'))
manifest = {}
for name, version, target, patch in [
    ('github.com/boltdb/bolt', 'v1.3.1', 'bolt', 'bolt/bolt_64bit.go'),
    ('go.etcd.io/bbolt', 'v1.3.5', 'bbolt', 'bbolt/bbolt_loong64.go'),
]:
    module = json.loads(subprocess.check_output(['go', 'list', '-m', '-json', name], cwd=source, text=True))
    if module['Version'] != version or module.get('Replace'):
        raise SystemExit('Port patch needs review for ' + name)
    destination = stage / 'portable-modules' / target
    if destination.exists():
        shutil.rmtree(destination)
    shutil.copytree(module['Dir'], destination)
    for path in destination.rglob('*'):
        path.chmod(0o755 if path.is_dir() else 0o644)
    destination.chmod(0o755)
    if not (destination / 'go.mod').exists():
        (destination / 'go.mod').write_text('module ' + name + '\n\ngo 1.26.0\n')
    shutil.copyfile(ports / patch, destination / 'godhcp_portable.go')
    subprocess.run(['go', 'mod', 'edit', '-modfile=' + str(modfile),
                    '-replace=' + name + '=' + str(destination)], cwd=source, check=True)
    manifest[name] = {'upstream_version': version, 'patch': patch,
                     'patch_sha256': hashlib.sha256((ports / patch).read_bytes()).hexdigest()}
(stage / 'PORTABILITY.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(modfile)
