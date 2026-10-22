# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

FROM rockylinux:9
RUN dnf install -y systemd kernel-core kernel-modules dracut iproute python3 python3-pip libcap && dnf clean all
# Scapy is a client fixture only, never an application dependency.
RUN pip3 install --no-cache-dir scapy==2.6.1
COPY package.rpm /tmp/godhcp.rpm
RUN dnf install -y /tmp/godhcp.rpm libpcap && dnf clean all
COPY tests/systemd /opt/godhcp-lab
COPY tests/integration/dhcp_lab.py /opt/godhcp-lab/dhcp_lab.py
RUN python3 /opt/godhcp-lab/prepare.py && dracut --force --no-hostonly --add-drivers 'virtio_blk virtio_pci virtio_mmio' /boot/initramfs-lab.img $(ls /lib/modules | head -1)
