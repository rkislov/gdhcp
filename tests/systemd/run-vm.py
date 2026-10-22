#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

"""Exported distro filesystem -> ext4 -> full ARM64 Linux boot + kernel reboot."""
import json
import gzip
import struct
import shutil
from pathlib import Path
import subprocess as sp
import sys

out = Path('/results')
root = Path('/tmp/guest-root')
root.mkdir()
sp.run(['tar','-xpf','/input/rootfs.tar','-C',str(root)],check=True)
kernels=sorted((root/'boot').glob('vmlinuz-*')) or sorted((root/'lib/modules').glob('*/vmlinuz'))
assert kernels, 'no distro kernel image'
kernel=kernels[-1]
# Rocky's EFI zboot wrapper holds a compressed standard ARM64 Image.
# Decode the documented zimg payload for QEMU's direct -kernel boot.
data=kernel.read_bytes()
if data[4:8]==b'zimg':
    offset,size=struct.unpack_from('<II',data,8)
    payload=gzip.decompress(data[offset:offset+size])
    assert payload[56:60]==b'ARM\x64', 'unsupported zboot payload'
    kernel=Path('/tmp/kernel.Image');kernel.write_bytes(payload)
initrds = list((root/'boot').glob('initrd.img-*')) or list((root/'boot').glob('initramfs-lab.img'))
assert initrds, 'no boot initramfs'
image = '/tmp/guest.ext4'
with open(image,'wb') as f:
    f.truncate(4*1024**3)
sp.run(['mkfs.ext4','-q','-F','-d',str(root),image],check=True)
# There is no virtual NIC, forwarding, SSH or access to the host network.
cmd = ['qemu-system-aarch64','-machine','virt,gic-version=3','-cpu','cortex-a72','-smp','2','-m','1024',
       '-accel','tcg,thread=multi','-nodefaults','-display','none','-monitor','none','-serial','stdio',
       '-kernel',str(kernel),'-initrd',str(initrds[0]),'-append','root=/dev/vda rw console=ttyAMA0 panic=1',
       '-drive','if=none,id=root,file='+image+',format=raw','-device','virtio-blk-device,drive=root']
(out/'qemu-command.json').write_text(json.dumps(cmd,indent=2)+'\n')
with open(out/'console.log','w') as log:
    vm=sp.Popen(cmd,stdout=log,stderr=sp.STDOUT)
    try:
        code=vm.wait(timeout=900)
    except sp.TimeoutExpired:
        vm.kill();vm.wait()
        raise SystemExit('VM timeout; inspect console.log')
# Replay any committed ext4 journal transactions before reading with debugfs.
fsck=sp.run(['e2fsck','-f','-y',image])
assert fsck.returncode in (0,1), 'guest filesystem recovery failed'
extract=Path('/tmp/extracted');extract.mkdir()
sp.run(['debugfs','-R','rdump /var/lib/godhcp-lab /tmp/extracted',image],check=True)
sp.run(['debugfs','-R','rdump /results /tmp/extracted',image],check=True)
shutil.copytree(extract,out,dirs_exist_ok=True)
report=out/'godhcp-lab/report.json'
assert report.exists(), 'no guest report; inspect console.log'
data=json.loads(report.read_text())
for r in data['results']:
    print(r['status'],r['name'])
assert code==0 and len(data.get('boot_ids',[]))==2, 'no completed kernel reboot'
assert all(r['status']=='PASS' for r in data['results']), 'guest checks failed'
print('PASS complete VM systemd test:', data['kernel'])
