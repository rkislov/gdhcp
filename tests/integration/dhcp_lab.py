#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

"""Real Linux links, independent Scapy client and ISC dhclient/dhcrelay.

Runtime must be an isolated privileged Docker container (--network none).
Never execute this harness directly on an administrator's Linux host.
"""
import ipaddress
import json
from pathlib import Path
import random
import sqlite3
import signal
import subprocess as sp
import sys
import threading
import time

RESULTS = Path('/results')


def run(*args, **kw):
    return sp.run(list(args), check=True, text=True, capture_output=True, **kw).stdout


def ip(*args):
    return run('ip', *args)


def ns(name, *args):
    return run('ip', 'netns', 'exec', name, *args)


def worker(a):
    from scapy.all import Ether, IP, UDP, BOOTP, DHCP, AsyncSniffer, sendp, get_if_hwaddr, wrpcap
    iface, mode = a['iface'], a['mode']
    mac = a.get('mac', get_if_hwaddr(iface))
    chaddr = bytes.fromhex(mac.replace(':', ''))
    cid = b'\x01' + chaddr
    xid = random.randrange(1, 2**32)
    captured = []

    def exchange(kind, extra=(), ciaddr='0.0.0.0', expect=2, op=1):
        flags = 0x8000 if a.get('broadcast', True) else 0
        dest = a.get('server', '255.255.255.255') if ciaddr != '0.0.0.0' else '255.255.255.255'
        frame = (Ether(src=mac, dst='ff:ff:ff:ff:ff:ff') /
                 IP(src=ciaddr, dst=dest) / UDP(sport=68, dport=67) /
                 BOOTP(op=op, htype=1, hlen=6, xid=xid, chaddr=chaddr, flags=flags, ciaddr=ciaddr) /
                 DHCP(options=[('message-type', kind), ('client_id', cid), ('hostname', a.get('name', 'lab')), *extra, 'end']))
        ready = threading.Event()
        responses = []
        def received(p):
            if BOOTP in p and UDP in p and p[UDP].sport == 67 and p[BOOTP].op == 2 and p[BOOTP].xid == xid:
                responses.append(p)
        sniffer = AsyncSniffer(iface=iface, filter='udp and (port 67 or port 68)', store=True, started_callback=ready.set, prn=received)
        sniffer.start()
        assert ready.wait(3), 'sniffer failed to start'
        sendp(frame, iface=iface, verbose=False)
        deadline = time.monotonic() + (1 if expect is None else 3)
        while time.monotonic() < deadline:
            time.sleep(0.02)
            if responses:
                break
        packets = sniffer.stop()
        captured.extend(packets)
        replies = [p for p in packets if BOOTP in p and UDP in p and p[UDP].sport == 67 and p[BOOTP].op == 2 and p[BOOTP].xid == xid]
        if expect is None:
            assert not replies, 'unexpected reply to invalid request'
            return None
        assert replies, f'no DHCP reply for {kind}, flags={flags:#x}, ciaddr={ciaddr}'
        reply = replies[0]
        opts = dict(o for o in reply[DHCP].options if isinstance(o, tuple))
        assert opts['message-type'] == expect, f'expected {expect}, got {opts}'
        if expect == 6:
            assert reply[IP].dst == '255.255.255.255', f'NAK sent to {reply[IP].dst}'
            assert reply[BOOTP].ciaddr == '0.0.0.0', 'NAK ciaddr must be zero'
        return reply, opts

    try:
        if mode == 'dora':
            offer, opts = exchange('discover')
            addr, server = offer[BOOTP].yiaddr, opts['server_id']
            ack, opts = exchange('request', [('requested_addr', addr), ('server_id', server)], expect=5)
            assert ack[BOOTP].yiaddr == addr
            assert ipaddress.ip_address(addr) in ipaddress.ip_network(a['network'])
            assert opts['subnet_mask'] == '255.255.255.0'
            assert opts['router'] == a['gateway']
            if 'dns' in a:
                assert opts['name_server'] == a['dns'], f'DNS mismatch: {opts}'
            assert opts['lease_time'] == 120 and opts['renewal_time'] == 5
            print(json.dumps({'ip': addr, 'server': server, 'mac': mac}))
        elif mode == 'renew':
            reply, opts = exchange('request', ciaddr=a['ip'], expect=5)
            assert reply[BOOTP].yiaddr == a['ip']
            assert reply[IP].dst == a['ip'], 'renew ACK must be unicast to ciaddr'
            print(json.dumps({'ip': a['ip'], 'server': opts['server_id'], 'mac': mac}))
        elif mode == 'nak':
            exchange('request', ciaddr=a['ip'], expect=6)
            print('{}')
        elif mode == 'release':
            exchange('release', [('server_id', a['server'])], ciaddr=a['ip'], expect=None)
            print('{}')
        elif mode == 'decline':
            exchange('decline', [('requested_addr', a['ip']), ('server_id', a['server'])], expect=None)
            print('{}')
        elif mode == 'cross-vlan':
            exchange('request', [('requested_addr', a['ip']), ('server_id', a['server'])], expect=6)
            print('{}')
        elif mode == 'invalid-op':
            exchange('discover', expect=None, op=2)
            print('{}')
        else:
            raise AssertionError(mode)
    finally:
        wrpcap(str(RESULTS / (a['name'] + '.pcap')), captured)


def setup():
    for name in ('access', 'tagged', 'relay', 'remote'):
        ip('netns', 'add', name)
        ns(name, 'ip', 'link', 'set', 'lo', 'up')
    for root, peer, namespace in [('access0', 'eth0', 'access'), ('trunk0', 'eth0', 'tagged'), ('uplink', 'relayup', 'relay')]:
        ip('link', 'add', root, 'type', 'veth', 'peer', 'name', 'peer0')
        ip('link', 'set', 'peer0', 'netns', namespace)
        ns(namespace, 'ip', 'link', 'set', 'peer0', 'name', peer)
        ip('link', 'set', root, 'up')
        ns(namespace, 'ip', 'link', 'set', peer, 'up')
    ip('addr', 'add', '198.18.10.1/24', 'dev', 'access0')
    for vlan in (20, 30):
        ip('link', 'add', 'link', 'trunk0', 'name', f'trunk0.{vlan}', 'type', 'vlan', 'id', str(vlan))
        ip('addr', 'add', f'198.18.{vlan}.1/24', 'dev', f'trunk0.{vlan}')
        ip('link', 'set', f'trunk0.{vlan}', 'up')
        ns('tagged', 'ip', 'link', 'add', 'link', 'eth0', 'name', f'eth0.{vlan}', 'type', 'vlan', 'id', str(vlan))
        ns('tagged', 'ip', 'link', 'set', f'eth0.{vlan}', 'up')
    ip('addr', 'add', '10.255.0.1/30', 'dev', 'uplink')
    ns('relay', 'ip', 'addr', 'add', '10.255.0.2/30', 'dev', 'relayup')
    ns('relay', 'ip', 'route', 'add', 'default', 'via', '10.255.0.1')
    ip('link', 'add', 'relaydown', 'type', 'veth', 'peer', 'name', 'remote0')
    ip('link', 'set', 'relaydown', 'netns', 'relay')
    ip('link', 'set', 'remote0', 'netns', 'remote')
    ns('remote', 'ip', 'link', 'set', 'remote0', 'name', 'eth0')
    ns('remote', 'ip', 'link', 'set', 'eth0', 'up')
    ns('relay', 'ip', 'addr', 'add', '198.18.40.1/24', 'dev', 'relaydown')
    ns('relay', 'ip', 'link', 'set', 'relaydown', 'up')
    ns('relay', 'python3', '-c', "open('/proc/sys/net/ipv4/ip_forward', 'w').write('1')")
    ip('route', 'add', '198.18.40.0/24', 'via', '10.255.0.2')
    config = '''server:
  listen: ':67'
  server_id: 198.18.10.1
  authoritative: true
  lease_default: 120s
  lease_max: 120s
  interfaces:
    - {name: access0, mode: access, vlan: 10}
database:
  driver: sqlite
  dsn: /results/leases.db
api:
  listen: '127.0.0.1:8080'
web:
  enabled: false
logging:
  level: debug
  format: json
relay:
  enabled: true
  echo_option_82: true
  trusted_relays:
    - {giaddr: 198.18.40.1, subnet_id: net40}
'''
    config += 'vlans:\n' + ''.join(f'  - {{id: {v}, interface: trunk0.{v}, subnet_ref: net{v}}}\n' for v in (10, 20, 30, 40))
    config += 'subnets:\n' + ''.join(f'''  - id: net{v}
    network: 198.18.{v}.0/24
    range: [198.18.{v}.100, 198.18.{v}.110]
    gateway: 198.18.{v}.1
    dns: [192.0.2.53]
    vlan: {v}
    t1: 5s
    t2: 90s
''' for v in (10, 20, 30, 40))
    (RESULTS / 'config.yml').write_text(config)


def verify_recreated():
    """New container and process, same bind-mounted SQLite directory."""
    assert Path('/.dockerenv').exists(), 'This harness must only run inside Docker'
    setup()
    expected = json.loads((RESULTS / 'lease-snapshot.json').read_text())
    original = json.loads((RESULTS / 'original-client.json').read_text())
    # setup() writes the initial interface mapping again in this fresh namespace.
    with open(RESULTS / 'recreated-server.log', 'w') as log:
        server = sp.Popen(['godhcp', '-config', '/results/config.yml'], stdout=log, stderr=log)
        try:
            time.sleep(.5)
            assert server.poll() is None
            with sqlite3.connect(RESULTS / 'leases.db') as db:
                db.row_factory = sqlite3.Row
                actual = {r['ip']: dict(r) for r in db.execute('select * from leases')}
            assert actual == expected, 'leases changed after container recreation'
            assert original and actual[original['ip']]['state'] == 'bound'
            ns('access', 'ip', 'addr', 'replace', original['ip'] + '/24', 'dev', 'eth0')
            a = {'name': 'recreated-renew', 'mode': 'renew', 'iface': 'eth0', 'broadcast': False, **original}
            ns('access', 'python3', '/lab/dhcp_lab.py', 'client', json.dumps(a))
            a = {'name': 'recreated-new', 'mode': 'dora', 'iface': 'eth0', 'mac': '02:00:00:00:10:09', 'network': '198.18.10.0/24', 'gateway': '198.18.10.1'}
            new = json.loads(ns('access', 'python3', '/lab/dhcp_lab.py', 'client', json.dumps(a)))
            assert new['ip'] not in expected, 'active address reused after container recreation'
            result = {'name': 'fresh container preserves leases, renews original and avoids duplicates', 'status': 'PASS'}
            print('PASS', result['name'])
            (RESULTS / 'recreated-report.json').write_text(json.dumps(result, indent=2) + '\n')
        finally:
            server.terminate()
            server.wait(timeout=5)


def main():
    assert Path('/.dockerenv').exists(), 'This harness must only run inside Docker'
    RESULTS.mkdir(exist_ok=True)
    # A failed run must not leave a previous run's successful report behind.
    for name in ('report.json', 'recreated-report.json'):
        (RESULTS / name).unlink(missing_ok=True)
    for pattern in ('leases.db*', 'dhclient*'):
        for file in RESULTS.glob(pattern):
            file.unlink()
    setup()
    processes, results = [], []
    log = open(RESULTS / 'server.log', 'w')

    def start():
        p = sp.Popen(['godhcp', '-config', '/results/config.yml'], stdout=log, stderr=log)
        processes.append(p)
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            assert p.poll() is None, 'server exited, see server.log'
            if ':0043 ' in Path('/proc/net/udp').read_text():
                return p
            time.sleep(.05)
        raise AssertionError('server did not bind UDP 67')

    def check(name, fn):
        try:
            value = fn()
            results.append({'name': name, 'status': 'PASS'})
            print('PASS', name, flush=True)
            return value
        except Exception as exc:
            results.append({'name': name, 'status': 'FAIL', 'error': str(exc)})
            print('FAIL', name, str(exc), flush=True)
            return None

    def client(name, namespace='access', iface='eth0', mode='dora', **kw):
        v = {'iface': iface, 'mode': mode, 'name': name, 'network': '198.18.10.0/24', 'gateway': '198.18.10.1', **kw}
        try:
            out = ns(namespace, 'python3', '/lab/dhcp_lab.py', 'client', json.dumps(v))
            return json.loads(out)
        except sp.CalledProcessError as exc:
            raise AssertionError(exc.stderr.strip()) from exc

    def leases():
        with sqlite3.connect(RESULTS / 'leases.db') as db:
            db.row_factory = sqlite3.Row
            return {row['ip']: dict(row) for row in db.execute('select * from leases')}

    def renew(name, namespace, iface, lease):
        assert lease, 'DORA prerequisite failed'
        ns(namespace, 'ip', 'addr', 'replace', lease['ip'] + '/24', 'dev', iface)
        if namespace == 'remote':
            ns(namespace, 'ip', 'route', 'replace', 'default', 'via', '198.18.40.1')
        before = leases()[lease['ip']]['expires_at']
        value = client(name, namespace, iface, mode='renew', broadcast=False, **lease)
        assert leases()[lease['ip']]['expires_at'] > before, 'lease expiration was not extended'
        return value

    try:
        server = start()
        relaylog = open(RESULTS / 'relay.log', 'w')
        relay = sp.Popen(['ip', 'netns', 'exec', 'relay', 'dhcrelay', '-4', '-d', '-a', '-id', 'relaydown', '-iu', 'relayup', '10.255.0.1'], stdout=relaylog, stderr=relaylog)
        processes.append(relay)
        capture = sp.Popen(['tcpdump', '-U', '-n', '-i', 'trunk0', '-w', '/results/vlan-wire.pcap'], stdout=sp.DEVNULL, stderr=sp.DEVNULL)
        processes.append(capture)
        relaycap = sp.Popen(['tcpdump', '-U', '-n', '-i', 'uplink', '-w', '/results/relay-wire.pcap', 'udp', 'port', '67'], stdout=sp.DEVNULL, stderr=sp.DEVNULL)
        processes.append(relaycap)
        time.sleep(.4)
        direct = check('access DORA broadcast', lambda: client('access-broadcast'))
        check('access unicast renewal', lambda: renew('access-renew', 'access', 'eth0', direct))
        # A different MAC with no configured address or ARP entry.
        check('access DORA without broadcast flag', lambda: client('access-no-broadcast', mac='02:00:00:00:10:02', broadcast=False))
        check('direct NAK for invalid ciaddr is broadcast', lambda: client('invalid-ciaddr', mode='nak', ip='203.0.113.99', broadcast=False))
        check('BOOTREPLY is ignored', lambda: client('invalid-op', mode='invalid-op', mac='02:00:00:00:10:03'))
        def release_lifecycle():
            lease = client('release-bind', mac='02:00:00:00:10:05')
            ns('access', 'ip', 'addr', 'replace', lease['ip'] + '/24', 'dev', 'eth0')
            client('release', mode='release', **lease)
            assert leases()[lease['ip']]['state'] == 'released', 'RELEASE did not release lease'
            rebound = client('release-rebind', mac=lease['mac'])
            assert leases()[rebound['ip']]['state'] == 'bound', 'client cannot bind after RELEASE'
        check('RELEASE persists and client can bind again', release_lifecycle)
        def decline_lifecycle():
            lease = client('decline-bind', mac='02:00:00:00:10:06')
            client('decline', mode='decline', **lease)
            row = leases()[lease['ip']]
            assert row['state'] == 'declined', 'DECLINE did not quarantine address'
            replacement = client('decline-rebind', mac=lease['mac'])
            assert replacement['ip'] != lease['ip'], 'declined address was allocated again'
        check('DECLINE quarantines address and client gets replacement', decline_lifecycle)
        tagged = {}
        for v in (20, 30):
            tagged[v] = check(f'802.1Q VLAN {v} DORA', lambda v=v: client(f'vlan{v}', 'tagged', f'eth0.{v}', network=f'198.18.{v}.0/24', gateway=f'198.18.{v}.1'))
            check(f'802.1Q VLAN {v} renewal', lambda v=v: renew(f'vlan{v}-renew', 'tagged', f'eth0.{v}', tagged[v]))
        check('VLAN cannot request address from another VLAN', lambda: client('cross-vlan', 'tagged', 'eth0.20', mode='cross-vlan', ip='198.18.30.105', server='198.18.10.1'))
        remote = check('ISC relay DORA with Option 82', lambda: client('relay-dora', 'remote', network='198.18.40.0/24', gateway='198.18.40.1'))
        check('routed renewal after relay allocation', lambda: renew('relay-renew', 'remote', 'eth0', remote))
        def named_vlan():
            ip('link', 'set', 'trunk0.20', 'name', 'voice0')
            path = RESULTS / 'config.yml'
            path.write_text(path.read_text().replace('interface: trunk0.20', 'interface: voice0'))
            server.send_signal(signal.SIGHUP)
            time.sleep(.2)
            return client('named-vlan', 'tagged', 'eth0.20', network='198.18.20.0/24', gateway='198.18.20.1')
        check('VLAN with explicitly mapped interface name voice0', named_vlan)
        before = leases()
        server.terminate()
        server.wait(timeout=5)
        server = start()
        def persistence():
            after = leases()
            for addr, row in before.items():
                assert after[addr] == row, f'lease changed on restart: {addr}'
            for v, lease in [(10, direct), (20, tagged[20]), (30, tagged[30]), (40, remote)]:
                assert lease and after[lease['ip']]['state'] == 'bound', f'missing bound lease in VLAN {v}'
            new = client('post-restart-new', mac='02:00:00:00:10:04')
            assert new['ip'] not in before, 'active address was allocated to a second client'
        check('SQLite leases survive server restart without duplicate allocation', persistence)
        check('original client renews after restart', lambda: renew('post-restart-renew', 'access', 'eth0', direct))
        # Real distribution DHCP client, including automatic T1 renewal.
        def isc_client():
            script = RESULTS / 'dhclient-script'
            script.write_text('#!/bin/sh\necho "$reason $new_ip_address" >> /results/dhclient-events\nif [ "$reason" = BOUND ] || [ "$reason" = RENEW ] || [ "$reason" = REBOOT ]; then ip addr replace "$new_ip_address/24" dev "$interface"; fi\n')
            script.chmod(0o755)
            p = sp.Popen(['ip', 'netns', 'exec', 'access', 'dhclient', '-4', '-d', '-v', '-sf', str(script), '-lf', '/results/dhclient.leases', '-pf', '/results/dhclient.pid', 'eth0'], stdout=open(RESULTS / 'dhclient.log', 'w'), stderr=sp.STDOUT)
            processes.append(p)
            deadline = time.monotonic() + 25
            while time.monotonic() < deadline:
                events = (RESULTS / 'dhclient-events').read_text() if (RESULTS / 'dhclient-events').exists() else ''
                if 'BOUND ' in events and 'RENEW ' in events:
                    return
                assert p.poll() is None, 'ISC dhclient exited, see dhclient.log'
                time.sleep(.2)
            raise AssertionError('ISC dhclient did not bind and renew in 25 seconds; see dhclient.log')
        check('ISC dhclient obtains and automatically renews at T1', isc_client)
    finally:
        for p in reversed(processes):
            if p.poll() is None:
                p.terminate()
                try:
                    p.wait(timeout=5)
                except sp.TimeoutExpired:
                    p.kill()
                    p.wait()
        log.close()
    # Validate wire-level tags and Option 82 echoed to the relay (ISC strips it downstream).
    from scapy.all import rdpcap, Dot1Q, BOOTP, DHCP
    def wire():
        vlans = {int(p[Dot1Q].vlan) for p in rdpcap('/results/vlan-wire.pcap') if Dot1Q in p and BOOTP in p and p[BOOTP].op == 2}
        assert {20, 30}.issubset(vlans), f'no tagged replies on trunk: {vlans}'
        requests, echoed = {}, 0
        for p in rdpcap('/results/relay-wire.pcap'):
            if DHCP not in p:
                continue
            opt = dict(o for o in p[DHCP].options if isinstance(o, tuple)).get('relay_agent_information')
            if p[BOOTP].op == 1 and opt is not None:
                requests[p[BOOTP].xid] = opt
            elif p[BOOTP].op == 2 and p[BOOTP].xid in requests:
                assert opt == requests[p[BOOTP].xid], 'Option 82 was not echoed byte-for-byte'
                echoed += 1
        assert echoed >= 2, 'expected both OFFER and ACK from relay capture'
    check('wire capture contains actual VLAN tags and exact Option 82 echo', wire)
    (RESULTS / 'lease-snapshot.json').write_text(json.dumps(leases(), indent=2))
    (RESULTS / 'original-client.json').write_text(json.dumps(direct))
    report = {'platform': run('uname', '-a').strip(), 'server': run('godhcp', 'version').strip(), 'results': results}
    (RESULTS / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
    failed = sum(r['status'] != 'PASS' for r in results)
    print(f'{len(results) - failed}/{len(results)} passed; evidence: /results')
    return bool(failed)


if __name__ == '__main__':
    if len(sys.argv) > 1 and sys.argv[1] == 'client':
        worker(json.loads(sys.argv[2]))
    elif len(sys.argv) > 1 and sys.argv[1] == '--verify-recreated':
        verify_recreated()
    else:
        sys.exit(main())
