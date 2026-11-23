# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
FROM godhcp-systemd-debian:local
RUN apt-get update && apt-get install -y --no-install-recommends iptables iputils-arping && rm -rf /var/lib/apt/lists/*
COPY godhcp /usr/bin/godhcp
COPY scripts/godhcp-cluster-ip /usr/libexec/godhcp/godhcp-cluster-ip
COPY tests/cluster-vm/agent.py /opt/pair-lab/agent.py
COPY tests/active-backup/ip-hook.py /opt/pair-lab/ip-hook.py
RUN systemctl disable godhcp-lab.service godhcp-lab-network.service && mkdir -p /usr/libexec/godhcp
