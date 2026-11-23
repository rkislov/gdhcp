#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Independent DHCPv6 direct/relay client for the isolated two-VM bridge."""
import json
from pathlib import Path
import socket
import sys
from scapy.layers.dhcp6 import *

mode=sys.argv[1]
index=socket.if_nametoindex('br-pair')
cid=DHCP6OptClientId(duid=DUID_LL(lladdr='02:00:00:00:00:66'))
sock=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM);sock.bind(('::',546));sock.settimeout(4)
sock.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_MULTICAST_IF,index)
relay=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM);relay.bind(('fd42:10::2',547));relay.settimeout(4)

def exchange(msg,relayed,expected):
 if relayed:
  wrapped=DHCP6_RelayForward(linkaddr='fd42:20::1',peeraddr='fe80::2')/DHCP6OptIfaceId(ifaceid=b'cluster-lan')/DHCP6OptRelayMsg(message=msg)
  relay.sendto(bytes(wrapped),('fd42:10::1',547))
  response=DHCP6_RelayReply(relay.recv(8192))
  assert response.msgtype==13 and response[DHCP6OptIfaceId].ifaceid==b'cluster-lan'
  out=response[DHCP6OptRelayMsg].message
 else:
  sock.sendto(bytes(msg),('ff02::1:2',547,0,index));out=DHCP6_Reply(sock.recv(8192))
 assert out.msgtype==expected and out.trid==msg.trid,out.summary()
 return out

for relayed in (False,True):
 path=Path('/results/client6-'+str(relayed)+'.json')
 iaid=67 if relayed else 66
 if mode=='bind':
  adv=exchange(DHCP6_Solicit(trid=iaid)/cid/DHCP6OptIA_NA(iaid=iaid),relayed,2)
  addr=adv[DHCP6OptIAAddress].addr;duid=bytes(adv[DHCP6OptServerId].duid).hex()
  ia=DHCP6OptIA_NA(iaid=iaid,ianaopts=[DHCP6OptIAAddress(addr=addr)])
  rep=exchange(DHCP6_Request(trid=iaid)/cid/DHCP6OptServerId(duid=bytes.fromhex(duid))/ia,relayed,7)
  assert rep[DHCP6OptIAAddress].addr==addr
  path.write_text(json.dumps(dict(ip=addr,duid=duid)))
 else:
  saved=json.loads(path.read_text());addr=saved['ip'];duid=saved['duid']
  ia=DHCP6OptIA_NA(iaid=iaid,ianaopts=[DHCP6OptIAAddress(addr=addr)])
  request=DHCP6_Renew(trid=iaid)/cid/DHCP6OptServerId(duid=bytes.fromhex(duid))/ia
  if mode=='silent':
   try:exchange(request,relayed,7)
   except socket.timeout:continue
   raise AssertionError('DHCPv6 answered without quorum')
  rep=exchange(request,relayed,7)
  assert rep[DHCP6OptIAAddress].addr==addr and bytes(rep[DHCP6OptServerId].duid).hex()==duid
 print('PASS DHCPv6',mode,'relay' if relayed else 'direct',addr)
