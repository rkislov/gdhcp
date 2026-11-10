#!/bin/bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
# Disposable test-only domain; no host networking, ports or production credentials.
set -euo pipefail
rm -f /etc/samba/smb.conf
ip=$(python3 -c 'import socket; print(socket.gethostbyname(socket.gethostname()))')
samba-tool domain provision --realm=DHCP.TEST --domain=DHCP --server-role=dc --dns-backend=SAMBA_INTERNAL --adminpass='Test-only-Admin-739!' --host-name=dc --host-ip="$ip" --option='posix:eadb=/var/lib/samba/private/eadb.tdb' > /tmp/provision.log 2>&1 || { cat /tmp/provision.log; exit 1; }
cat > /etc/krb5.conf <<'KRB'
[libdefaults]
 default_realm = DHCP.TEST
 dns_lookup_realm = false
 dns_lookup_kdc = false
 rdns = false
[realms]
 DHCP.TEST = {
  kdc = 127.0.0.1
  admin_server = 127.0.0.1
 }
KRB
samba -i -d 3 > /tmp/samba.log 2>&1 &
samba_pid=$!
trap 'status=$?; if test "$status" != 0; then tail -60 /tmp/samba.log; fi; kill "$samba_pid" 2>/dev/null || true' EXIT
for attempt in $(seq 1 60); do
 if samba-tool dns zonelist 127.0.0.1 -U 'Administrator%Test-only-Admin-739!' > /tmp/zones 2>/dev/null; then break; fi
 sleep 1
done
samba-tool user create dhcp-dns 'Test-only-Service-739!'
samba-tool group addmembers DnsAdmins dhcp-dns
samba-tool domain exportkeytab /tmp/dhcp-dns.keytab --principal=dhcp-dns
samba-tool dns zonecreate 127.0.0.1 hosts.dhcp.test -U 'Administrator%Test-only-Admin-739!'
samba-tool dns zonecreate 127.0.0.1 2.0.192.in-addr.arpa -U 'Administrator%Test-only-Admin-739!'
samba-tool dns add 127.0.0.1 hosts.dhcp.test foreign A 192.0.2.250 -U 'Administrator%Test-only-Admin-739!'
samba-tool dns add 127.0.0.1 2.0.192.in-addr.arpa 20 PTR legacy.hosts.dhcp.test -U 'Administrator%Test-only-Admin-739!'
useradd --system --no-create-home godhcp
chown root:godhcp /tmp/dhcp-dns.keytab
chmod 0640 /tmp/dhcp-dns.keytab
runuser -u godhcp -- env GODHCP_DDNS_GSS_TEST=1 /test/ddns-integration.test -test.v -test.run TestSambaDNSLifecycle
