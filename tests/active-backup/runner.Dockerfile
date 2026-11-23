# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
FROM godhcp-systemd-runner:local
RUN apt-get update && apt-get install -y --no-install-recommends iproute2 python3-scapy openssl libpcap0.8 && rm -rf /var/lib/apt/lists/*
