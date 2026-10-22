#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Check project attribution, license metadata and shipped dependency notices."""
import csv
import json
from pathlib import Path
import subprocess

ROOT=Path(__file__).resolve().parent.parent
AUTHOR='Кислов Роман Сергеевич'
errors=[]

def check(ok,message):
    if not ok:errors.append(message)

for filename in ('LICENSE','NOTICE','README.md','internal/version/version.go'):
    check(AUTHOR in (ROOT/filename).read_text(),f'{filename}: author missing')
check('Apache License' in (ROOT/'LICENSE').read_text(),'Apache license missing')
package=json.loads((ROOT/'package.json').read_text())
check(package.get('license')=='Apache-2.0','npm license must be Apache-2.0')
check(package.get('author')==AUTHOR,'npm author must use full name')
check(json.loads((ROOT/'package-lock.json').read_text())['packages'][''].get('license')=='Apache-2.0','npm lock license missing')
meta=(ROOT/'deploy/nfpm.yaml').read_text()
check('license: Apache-2.0' in meta and f'maintainer: "{AUTHOR}"' in meta,'package author/license metadata mismatch')
source_types={'.go','.js','.cjs','.ts','.tsx','.yml','.yaml','.sql','.py','.sh','.html','.css','.service'}
for folder in ('cmd','internal','configs','api','deploy','scripts','tests'):
    for path in (ROOT/folder).rglob('*'):
        if not path.is_file() or (path.suffix not in source_types and 'Dockerfile' not in path.name):continue
        if path==ROOT/'internal/webui/static/style.css':
            check('tailwindcss' in path.read_text()[:200] and 'MIT License' in path.read_text()[:200],'generated CSS lost upstream MIT notice')
            continue
        head=path.read_text()[:1000]
        check(AUTHOR in head,f'{path.relative_to(ROOT)}: author missing')
        check('Apache' in head,f'{path.relative_to(ROOT)}: Apache notice missing')
for arch in ('amd64','arm64'):
    rows=list(csv.reader((ROOT/f'third_party/go-licenses-{arch}.csv').open()))
    check(bool(rows),f'{arch}: empty dependency report')
    for name,url,license_id in rows:
        check(license_id in {'MIT','BSD-3-Clause','Apache-2.0'},f'{name}: review license {license_id}')
        if name=='github.com/kislovrs/godhcp':continue
        directory=ROOT/'third_party/licenses/go'/name
        boundary=ROOT/'third_party/licenses/go'
        while directory!=boundary and not any(directory.glob('LICENSE*')):directory=directory.parent
        check(directory!=boundary,f'{name}: dependency license text missing')
for filename in ('go/modernc.org/libc/LICENSE-3RD-PARTY.md','go/modernc.org/sqlite/LICENSE-3RD-PARTY.md','go-standard/LICENSE','tailwindcss/LICENSE','dejavu/LICENSE'):
    check((ROOT/'third_party/licenses'/filename).is_file(),f'missing third-party notice: {filename}')
if errors:raise SystemExit('\n'.join(errors))
print('PASS: Apache-2.0; '+AUTHOR+'; source headers, package metadata and Linux dependency notices')
