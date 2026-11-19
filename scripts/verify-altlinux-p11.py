#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Check RPM architecture, extracted ELF, helpers and binary provenance."""
import hashlib
import json
import pathlib
import struct
import subprocess

root = pathlib.Path(__file__).resolve().parents[1]
version = next(line.split('"')[1] for line in (root/'internal/version/version.go').read_text().splitlines() if 'Version = ' in line)
packages = root / 'dist/altlinux-p11' / version
output = root / 'output/altlinux-p11' / version / 'verified-rpms'
output.mkdir(parents=True, exist_ok=True)
arches = {'x86_64': ('amd64', 62), 'aarch64': ('arm64', 183), 'i586': ('386', 3),
          'loongarch64': ('loong64', 258), 'riscv64': ('riscv64', 243)}
report = {}
for arch, (goarch, machine) in arches.items():
    filename = f'godhcp-{version}-alt1.p11.{arch}.rpm'
    script = '''set -eu
pkg=/packages/$1
arch=$2
test "$(rpm -qp --qf '%{ARCH}' "$pkg")" = "$arch"
rpm -qpl "$pkg" > /verify/$arch.files
for required in /usr/libexec/godhcp/godhcp-fence-peer /usr/libexec/godhcp/godhcp-cluster-ip /usr/share/doc/godhcp/godhcp-alt-p11-guide.pdf; do
    grep -Fx "$required" /verify/$arch.files
done
mkdir -p /tmp/extract
cd /tmp/extract
rpm2cpio "$pkg" | cpio -idm --quiet --no-absolute-filenames
cp usr/bin/godhcp /verify/$arch.elf
'''
    subprocess.run(['docker', 'run', '--rm', '--network', 'none',
                    '-v', str(packages)+':/packages:ro', '-v', str(output)+':/verify',
                    'godhcp-alt-p11-builder:local', 'sh', '-ec', script, 'verify', filename, arch], check=True,
                   stdout=subprocess.DEVNULL)
    data = (output/(arch+'.elf')).read_bytes()
    assert data[:4] == b'\x7fELF' and data[5] == 1
    assert struct.unpack_from('<H', data, 18)[0] == machine, arch
    assert data[4] == (1 if arch == 'i586' else 2)
    assert data == (packages/f'godhcp_{version}_linux_{goarch}').read_bytes(), arch
    installed = packages/('verified-'+arch)
    if arch in ('x86_64', 'aarch64', 'i586'):
        assert installed.read_bytes() == data, 'installed binary mismatch: '+arch
    info = subprocess.check_output(['go', 'version', '-m', str(output/(arch+'.elf'))], text=True)
    assert 'vcs.modified=false' in info, arch
    report[arch] = {'rpm': filename, 'rpm_sha256': hashlib.sha256((packages/filename).read_bytes()).hexdigest(),
                    'binary_sha256': hashlib.sha256(data).hexdigest(), 'elf_machine': machine,
                    'installation_test': arch in ('x86_64', 'aarch64', 'i586'),
                    'portability_additions': arch in ('loongarch64', 'riscv64')}
    print('PASS RPM architecture, ELF, payload and provenance:', arch)
(packages/'VERIFICATION.json').write_text(json.dumps(report, indent=2)+'\n')
