# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
FROM godhcp-ddns-samba-test:local
RUN apt-get update && apt-get install -y --no-install-recommends iputils-arping python3-scapy isc-dhcp-client isc-dhcp-relay tcpdump && rm -rf /var/lib/apt/lists/*
COPY godhcp /usr/local/bin/godhcp
COPY tests/integration /lab
ENTRYPOINT ["python3", "-u", "/lab/cluster_lab.py"]
