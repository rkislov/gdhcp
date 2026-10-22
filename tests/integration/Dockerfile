# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

FROM debian:12-slim
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends iproute2 python3 python3-scapy isc-dhcp-client isc-dhcp-relay tcpdump ca-certificates && rm -rf /var/lib/apt/lists/*
COPY godhcp /usr/local/bin/godhcp
COPY tests/integration /lab
ENTRYPOINT ["python3", "-u", "/lab/dhcp_lab.py"]
