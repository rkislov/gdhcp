# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

FROM debian:12-slim
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends qemu-system-arm e2fsprogs python3 ca-certificates && rm -rf /var/lib/apt/lists/*
