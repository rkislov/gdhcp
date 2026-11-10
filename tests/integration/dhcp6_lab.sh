#!/bin/bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
# Run in a disposable Linux container with --network none --privileged.
set -eu
ip netns add client
ip link add srv6 type veth peer name cli6
ip link set cli6 netns client
ip link set srv6 up
ip -n client link set lo up
ip -n client link set cli6 up
ip addr add fd42:10::1/64 dev srv6 nodad
ip -n client addr add fd42:10::2/64 dev cli6 nodad
# Use explicit link-local addresses even on Docker hosts with autoconfiguration disabled.
ip addr add fe80::1/64 dev srv6 nodad
ip -n client addr add fe80::2/64 dev cli6 nodad
cat >/tmp/config6.yml <<'YAML'
server:
  listen: "-"
database:
  driver: sqlite
  dsn: /tmp/leases6.db
api:
  listen: "127.0.0.1:8181"
dhcpv6:
  enabled: true
  preferred_seconds: 40
  valid_seconds: 60
  pools:
    - interface: srv6
      prefix: fd42:10::/64
      start: fd42:10::100
      end: fd42:10::102
    - interface: srv6
      prefix: fd42:20::/64
      start: fd42:20::100
      end: fd42:20::102
      relays: ["fd42:10::2"]
  dns: ["fd42:10::53"]
YAML
start() {
 godhcp -config /tmp/config6.yml >/tmp/server6.log 2>&1 &
 pid=$!
 for attempt in $(seq 1 50); do
   if grep -q 'dhcpv6 listening' /tmp/server6.log; then return; fi
   if ! kill -0 "$pid"; then cat /tmp/server6.log; exit 1; fi
   sleep .1
 done
 cat /tmp/server6.log; exit 1
}
start
trap 'kill "$pid" 2>/dev/null || true' EXIT
# A stock ISC client must receive a usable address on the multicast link.
ip netns exec client timeout 25 dhclient -6 -1 -v -lf /tmp/isc6.leases -pf /tmp/isc6.pid cli6
grep -Eq 'iaaddr fd42:10::10[0-2]' /tmp/isc6.leases
ip -n client -6 addr show dev cli6 | grep -Eq "inet6 fd42:10::10[0-2]/"
ip netns exec client dhclient -6 -x -pf /tmp/isc6.pid -lf /tmp/isc6.leases cli6
ip -n client addr replace fd42:10::2/64 dev cli6 nodad
ip netns exec client python3 /lab/dhcp6_wire.py bind
kill "$pid"; wait "$pid"
start
ip netns exec client python3 /lab/dhcp6_wire.py renew
