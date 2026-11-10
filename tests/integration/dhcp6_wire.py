# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Cross a Linux veth with independently encoded Scapy DHCPv6 messages."""
import json
import socket
import sys
from pathlib import Path
from scapy.layers.dhcp6 import (
    DHCP6_Advertise, DHCP6_Confirm, DHCP6_Decline, DHCP6_InfoRequest, DHCP6_Rebind,
    DHCP6_RelayForward, DHCP6_RelayReply, DHCP6_Release, DHCP6_Renew,
    DHCP6_Reply, DHCP6_Request, DHCP6_Solicit, DHCP6OptClientId, DHCP6OptDNSServers,
    DHCP6OptIAAddress, DHCP6OptIA_NA, DHCP6OptIfaceId, DHCP6OptOptReq,
    DHCP6OptRelayMsg, DHCP6OptServerId, DHCP6OptStatusCode, DUID_LL,
)

index = socket.if_nametoindex("cli6")
cid = DHCP6OptClientId(duid=DUID_LL(lladdr="02:00:00:00:00:42"))
trans = 0x123456
group = ("ff02::1:2", 547, 0, index)
saved = Path("/tmp/godhcp-client6.json")
sock = socket.socket(socket.AF_INET6, socket.SOCK_DGRAM)
sock.settimeout(4)
sock.bind(("::", 546))
sock.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_MULTICAST_IF, index)

def exchange(msg, cls=DHCP6_Reply):
    sock.sendto(bytes(msg), group)
    out = cls(sock.recv(4096))
    assert out.trid == trans and out.msgtype == cls().msgtype, out.summary()
    return out

if sys.argv[1] == "bind":
    adv = exchange(DHCP6_Solicit(trid=trans)/cid/DHCP6OptIA_NA(iaid=42), DHCP6_Advertise)
    server = adv[DHCP6OptServerId].duid
    addr = adv[DHCP6OptIAAddress].addr
    ia = DHCP6OptIA_NA(iaid=42, ianaopts=[DHCP6OptIAAddress(addr=addr)])
    rep = exchange(DHCP6_Request(trid=trans)/cid/DHCP6OptServerId(duid=server)/ia/DHCP6OptOptReq(reqopts=[23]))
    assert rep[DHCP6OptIAAddress].addr == addr
    assert rep[DHCP6OptDNSServers].dnsservers == ["fd42:10::53"]
    saved.write_text(json.dumps([addr, bytes(server).hex()]))
    print("PASS multicast Solicit, Advertise, Request, Reply, DNS")
else:
    addr, server_hex = json.loads(saved.read_text())
    server = DHCP6OptServerId(duid=bytes.fromhex(server_hex))
    ia = DHCP6OptIA_NA(iaid=42, ianaopts=[DHCP6OptIAAddress(addr=addr)])
    for pkt in (DHCP6_Renew(trid=trans)/cid/server/ia, DHCP6_Rebind(trid=trans)/cid/ia):
        rep = exchange(pkt)
        assert rep[DHCP6OptIAAddress].addr == addr and bytes(rep[DHCP6OptServerId].duid).hex() == server_hex
    assert exchange(DHCP6_Confirm(trid=trans)/cid/ia)[DHCP6OptStatusCode].statuscode == 0
    assert exchange(DHCP6_Release(trid=trans)/cid/server/ia)[DHCP6OptStatusCode].statuscode == 0
    assert exchange(DHCP6_Renew(trid=trans)/cid/server/ia)[DHCP6OptStatusCode].statuscode == 3
    print("PASS restart, stable DUID, Renew, Rebind, Confirm, Release")
    relay = socket.socket(socket.AF_INET6, socket.SOCK_DGRAM)
    relay.bind(("fd42:10::2",547))
    relay.settimeout(4)
    inner = DHCP6_Solicit(trid=trans)/cid/DHCP6OptIA_NA(iaid=55)
    outer = DHCP6_RelayForward(linkaddr="fd42:20::1", peeraddr="fe80::a")/DHCP6OptIfaceId(ifaceid=b"lan")/DHCP6OptRelayMsg(message=inner)
    relay.sendto(bytes(outer),("fd42:10::1",547))
    reply = DHCP6_RelayReply(relay.recv(4096))
    assert reply.msgtype == 13 and reply[DHCP6OptIfaceId].ifaceid == b"lan"
    offer = reply[DHCP6OptRelayMsg].message
    assert offer.trid == trans and offer[DHCP6OptIAAddress].addr.startswith("fd42:20::")
    print("PASS relay link selection, Relay-reply, Interface-ID")
