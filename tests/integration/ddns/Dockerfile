# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
FROM debian:12-slim
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends samba samba-vfs-modules samba-ad-provision samba-dsdb-modules winbind krb5-user bind9-dnsutils bind9 python3 iproute2 ca-certificates && rm -rf /var/lib/apt/lists/*
COPY setup-samba.sh /setup-samba.sh
ENTRYPOINT ["/bin/bash", "/setup-samba.sh"]
