#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Inspect final DEB/RPM/TAR metadata and bundled license materials."""
import io
from pathlib import Path
import subprocess as sp
import sys
import tarfile

kind=sys.argv[1]
directory=Path(sys.argv[2]) if len(sys.argv)>2 else Path('/packages')
author='Кислов Роман Сергеевич'
notices=['LICENSE','NOTICE','third_party/licenses/go/modernc.org/libc/LICENSE-3RD-PARTY.md','third_party/licenses/go/modernc.org/sqlite/LICENSE-3RD-PARTY.md','third_party/licenses/go-standard/LICENSE','third_party/licenses/tailwindcss/LICENSE','third_party/licenses/dejavu/LICENSE']
packages=sorted(directory.glob('*.tar.gz' if kind=='tar' else '*.'+kind))
assert len(packages)==2, 'expected amd64 and arm64 packages'
for package in packages:
    prefix='' if kind=='tar' else 'usr/share/licenses/godhcp/'
    if kind=='rpm':
        metadata=sp.check_output(['rpm','-qp','--qf','%{LICENSE}\n%{PACKAGER}\n%{VENDOR}',str(package)],text=True).splitlines()
        assert metadata==['Apache-2.0',author,author], metadata
        names=set(sp.check_output(['rpm','-qpl',str(package)],text=True).splitlines())
        assert all('/'+prefix+f in names for f in notices)
    else:
        if kind=='deb':
            assert sp.check_output(['dpkg-deb','-f',str(package),'Maintainer'],text=True).strip()==author
            stream=io.BytesIO(sp.check_output(['dpkg-deb','--fsys-tarfile',str(package)]))
            archive=tarfile.open(fileobj=stream)
        else:
            archive=tarfile.open(package)
        with archive:
            members={m.name.lstrip('./'):m for m in archive.getmembers()}
            assert all(prefix+f in members for f in notices), package
            license_text=archive.extractfile(members[prefix+'LICENSE']).read().decode()
            notice=archive.extractfile(members[prefix+'NOTICE']).read().decode()
            assert 'Apache License' in license_text and 'Version 2.0' in license_text
            assert author in notice
    print('PASS',package.name,': Apache-2.0, full author and all required notices')
