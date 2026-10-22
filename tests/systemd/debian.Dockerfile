# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

FROM debian:12-slim
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends systemd-sysv linux-image-arm64 iproute2 python3 python3-scapy libcap2-bin procps && rm -rf /var/lib/apt/lists/*
COPY package.deb /tmp/godhcp.deb
RUN dpkg -i /tmp/godhcp.deb
COPY tests/systemd /opt/godhcp-lab
COPY tests/integration/dhcp_lab.py /opt/godhcp-lab/dhcp_lab.py
RUN python3 /opt/godhcp-lab/prepare.py
