# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

"""Prepare a disposable VM root filesystem; called only during image build."""
from pathlib import Path
import subprocess

Path('/etc/fstab').write_text('/dev/vda / ext4 defaults 0 1\n')
Path('/etc/machine-id').write_text('')
Path('/etc/godhcp/config.yml').write_text('''server:
  listen: ':67'
  server_id: 198.18.10.1
  authoritative: true
  lease_default: 120s
  lease_max: 120s
  interfaces:
    - {name: access0, mode: access, vlan: 10}
database:
  driver: sqlite
  dsn: /var/lib/godhcp/leases.db
api:
  listen: '127.0.0.1:8080'
  auth:
    jwt_secret: '${JWT_SECRET}'
web:
  enabled: false
vlans:
  - {id: 10, interface: access0, subnet_ref: net10}
subnets:
  - id: net10
    network: 198.18.10.0/24
    range: [198.18.10.100, 198.18.10.110]
    gateway: 198.18.10.1
    dns: [192.0.2.53]
    t1: 5s
    t2: 90s
    vlan: 10
logging:
  level: debug
  format: json
''')
Path('/etc/godhcp/godhcp.env').write_text('JWT_SECRET=systemd-lab-secret-not-a-production-key\n')
subprocess.run(['chown','godhcp:godhcp','/etc/godhcp/config.yml'],check=True)
Path('/etc/godhcp/config.yml').chmod(0o640)
Path('/etc/godhcp/godhcp.env').chmod(0o600)
Path('/etc/systemd/system/godhcp-lab-network.service').write_text('''[Unit]
Description=Isolated DHCP test link
Before=godhcp.service
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /opt/godhcp-lab/guest.py network
RemainAfterExit=true
[Install]
WantedBy=multi-user.target
''')
Path('/etc/systemd/system/godhcp-lab.service').write_text('''[Unit]
Description=GoDHCP systemd VM acceptance test
After=godhcp.service godhcp-lab-network.service
Wants=godhcp.service godhcp-lab-network.service
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 -u /opt/godhcp-lab/guest.py
StandardOutput=journal+console
StandardError=journal+console
TimeoutStartSec=300
[Install]
WantedBy=multi-user.target
''')
subprocess.run(['systemctl','enable','godhcp.service','godhcp-lab-network.service','godhcp-lab.service'],check=True)
# No waits on real hardware or networking in the disconnected virtual machine.
subprocess.run(['systemctl','mask','systemd-networkd-wait-online.service','NetworkManager-wait-online.service'],check=True)
