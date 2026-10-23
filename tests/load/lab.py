#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Load real UDP sockets inside an isolated Docker network namespace.

The server and generator share a four-vCPU container; these are short virtual
network measurements, not a hardware or 24-hour acceptance test.
"""
import argparse
import concurrent.futures
import ipaddress
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess as sp
import time
import threading
import urllib.request

OUT = Path('/results')

def run(*args):
    return sp.check_output(args, text=True).strip()

def wait_ready(process):
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError('server exited; see server log')
        try:
            with urllib.request.urlopen('http://127.0.0.1:8080/readyz', timeout=.3) as r:
                if r.status == 200:
                    return
        except (OSError, TimeoutError):
            pass
        time.sleep(.05)
    raise RuntimeError('readiness timeout')

def stop(process):
    process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=10)
    except sp.TimeoutExpired:
        process.kill()
        process.wait()
        raise RuntimeError('server failed graceful shutdown')

def main():
    assert Path('/.dockerenv').exists(), 'container only; do not run on host'
    args = argparse.ArgumentParser()
    args.add_argument('--seconds', type=int, default=10)
    args.add_argument('--clients', type=int, default=100000)
    args.add_argument('--profile', action='store_true')
    args.add_argument('--case', action='append', help='run named mode-kind-rate scenario; may be repeated')
    args.add_argument('--diagnostics', action='store_true')
    args.add_argument('--trace', action='store_true')
    opts = args.parse_args()
    OUT.mkdir(exist_ok=True)
    (OUT/'report.json').unlink(missing_ok=True)
    for cmd in [
        ('ip','netns','add','clients'),
        ('ip','link','add','load0','type','veth','peer','name','peer0'),
        ('ip','link','set','peer0','netns','clients'),
        ('ip','addr','add','198.18.0.1/30','dev','load0'),
        ('ip','link','set','load0','up'),
        ('ip','netns','exec','clients','ip','link','set','lo','up'),
        ('ip','netns','exec','clients','ip','addr','add','198.18.0.2/30','dev','peer0'),
        ('ip','netns','exec','clients','ip','link','set','peer0','up'),
        ('ip','route','add','198.18.0.0/15','via','198.18.0.2','dev','load0'),
    ]:
        run(*cmd)
    for prefix in ipaddress.summarize_address_range(ipaddress.IPv4Address('198.18.0.10'), ipaddress.IPv4Address('198.19.255.254')):
        run('ip','netns','exec','clients','ip','route','add','local',str(prefix),'dev','lo','table','local')
    config = '''server:
  listen: ':67'
  server_id: 198.18.0.1
  authoritative: true
  ping_check: false
  lease_default: 1h
  lease_max: 2h
  interfaces:
    - {name: load0, mode: access, vlan: 100}
database:
  driver: sqlite
  dsn: /results/leases.db
api:
  listen: '127.0.0.1:8080'
web:
  enabled: false
logging:
  level: error
  format: json
relay:
  enabled: true
  require_option_82: true
  trusted_relays:
    - {giaddr: 198.18.0.2, remote_id: loadrelay, subnet_id: load}
vlans:
  - {id: 100, interface: load0, subnet_ref: load}
subnets:
  - id: load
    network: 198.18.0.0/15
    range: [198.18.0.10, 198.19.255.254]
    gateway: 198.18.0.1
    vlan: 100
'''
    (OUT/'config.yml').write_text(config)
    for p in OUT.glob('leases.db*'):
        p.unlink()
    with (OUT/'seed.log').open('w') as log:
        server = sp.Popen(['godhcp','-config',str(OUT/'config.yml')],stdout=log,stderr=log,env={**os.environ,'GOMAXPROCS':'4'})
        try:
            wait_ready(server)
        finally:
            stop(server)
    now = time.time()
    with sqlite3.connect(OUT/'leases.db') as db:
        def seed_rows():
            for i in range(opts.clients):
                ip = str(ipaddress.IPv4Address(int(ipaddress.IPv4Address('198.18.0.10'))+i))
                mac = f'02:00:00:{i>>16:02x}:{(i>>8)&255:02x}:{i&255:02x}'
                yield (ip,mac,'load',100,'bound',time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime(now+7200)),time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime(now)))
        db.executemany('INSERT INTO leases(ip,mac,subnet_id,vlan_id,state,expires_at,created_at) VALUES(?,?,?,?,?,?,?)', seed_rows())
    baseline = (OUT/'leases.db').read_bytes()
    cases = [('direct','renew',100),('direct','renew',5000),('relay','renew',100),('relay','renew',10000),('direct','inform',5000),('relay','inform',10000)]
    if opts.case:
        cases = [case for case in cases if '-'.join(map(str, case)) in opts.case]
        if not cases:
            raise ValueError('unknown --case')
    report = {'profile_enabled':opts.profile, 'trace_enabled':opts.trace, 'diagnostics_enabled':opts.diagnostics, 'kernel':run('uname','-a'),'version':run('godhcp','version'), 'cpu_limit':'4 shared vCPU, server GOMAXPROCS=4, generator GOMAXPROCS=1', 'cpu_model':Path('/proc/cpuinfo').read_text(), 'memory_limit_bytes':Path('/sys/fs/cgroup/memory.max').read_text().strip(), 'duration_seconds':opts.seconds,'seeded_bound_leases':opts.clients,'sqlite_pragmas':['journal_mode=WAL','synchronous=NORMAL'], 'logging':'error','ping_check':False,'scope':'UDP renewals with SQLite commits and INFORM control; not DORA throughput or 24h soak','results':[]}
    for mode,kind,rate in cases:
        name = f'{mode}-{kind}-{rate}'
        for p in OUT.glob('leases.db*'):
            p.unlink()
        (OUT/'leases.db').write_bytes(baseline)
        with (OUT/(name+'.log')).open('w') as log:
            server = sp.Popen(['godhcp','-config',str(OUT/'config.yml')],stdout=log,stderr=log,env={**os.environ,'GOMAXPROCS':'4','GODHCP_LOAD_PROFILE':'1' if opts.profile or opts.trace else '0','GODEBUG':'gctrace=1' if opts.diagnostics else '', 'GODHCP_COMMIT_LOG':str(OUT/(name+'.commits.jsonl')) if opts.diagnostics else ''})
            try:
                wait_ready(server)
                profile_pool = None
                profile_future = None
                if opts.profile or opts.trace:
                    def capture_profile():
                        endpoint = 'trace' if opts.trace else 'profile'
                        suffix = '.trace' if opts.trace else '.cpu.pprof'
                        with urllib.request.urlopen('http://127.0.0.1:6060/debug/pprof/'+endpoint+'?seconds='+str(opts.seconds),timeout=opts.seconds+20) as response:
                            (OUT/(name+suffix)).write_bytes(response.read())
                    profile_pool = concurrent.futures.ThreadPoolExecutor(max_workers=1)
                    profile_future = profile_pool.submit(capture_profile)
                    time.sleep(.2)
                monitor_stop = threading.Event()
                monitor_samples = []
                def monitor():
                    while not monitor_stop.is_set():
                        monitor_samples.append({'unix_ns':time.time_ns(),'cpu_stat':Path('/sys/fs/cgroup/cpu.stat').read_text(), 'wal_bytes':(OUT/'leases.db-wal').stat().st_size if (OUT/'leases.db-wal').exists() else 0})
                        monitor_stop.wait(.02)
                monitor_thread = None
                if opts.diagnostics:
                    monitor_thread = threading.Thread(target=monitor,daemon=True)
                    monitor_thread.start()
                try:
                    before = run('cat',f'/proc/{server.pid}/stat')
                    raw = sp.check_output(['ip','netns','exec','clients','env','GOMAXPROCS=1','godhcp-load','-mode',mode,'-kind',kind,'-rate',str(rate),'-seconds',str(opts.seconds),'-clients',str(opts.clients)],text=True,timeout=opts.seconds+40)
                    if profile_future is not None:
                        profile_future.result()
                finally:
                    monitor_stop.set()
                    if monitor_thread is not None:
                        monitor_thread.join()
                        (OUT/(name+'.monitor.json')).write_text(json.dumps(monitor_samples))
                    if profile_pool is not None:
                        profile_pool.shutdown()
                result = json.loads(raw)
                after = run('cat',f'/proc/{server.pid}/stat')
                # /proc comm can contain spaces; fields following final ')' start at field 3.
                a,b = before.rsplit(')',1)[1].split(),after.rsplit(')',1)[1].split()
                result['server_cpu_seconds'] = (int(b[11])+int(b[12])-int(a[11])-int(a[12]))/os.sysconf('SC_CLK_TCK')
                result['server_status'] = Path(f'/proc/{server.pid}/status').read_text()
                db = sqlite3.connect(OUT/'leases.db')
                rows = db.execute('SELECT count(*),count(distinct ip),count(distinct mac) FROM leases').fetchone()
                result['sqlite_integrity_check'] = db.execute('PRAGMA integrity_check').fetchone()[0]
                updated = 0
                identities_ok = True
                relay_metadata_ok = True
                for ip, mac, state, expires, giaddr, circuit, remote in db.execute('SELECT ip,mac,state,expires_at,giaddr,circuit_id,remote_id FROM leases'):
                    identity = int(mac.replace(':',''),16) & 0xffffff
                    identities_ok &= ip == str(ipaddress.IPv4Address(int(ipaddress.IPv4Address('198.18.0.10'))+identity)) and state == 'bound'
                    if expires != time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime(now+7200)):
                        updated += 1
                        if mode == 'relay':
                            relay_metadata_ok &= giaddr == '198.18.0.2' and circuit == 'load:100' and remote == 'loadrelay'
                expected_updates = min(opts.clients, result['sent']) if kind == 'renew' else 0
                result['updated_leases'] = updated
                result['expected_updated_leases_if_all_replied'] = expected_updates
                result['lease_identities_ok'] = identities_ok
                result['relay_metadata_ok'] = relay_metadata_ok
                result['committed_updates_ok'] = updated == expected_updates if result['valid_replies'] == result['sent'] else None
                db.close()
                result['database_counts'] = dict(zip(('leases','unique_ip','unique_mac'),rows))
                result['database_integrity_ok'] = rows == (opts.clients,opts.clients,opts.clients) and identities_ok and relay_metadata_ok and result['sqlite_integrity_check']=='ok' and result['committed_updates_ok'] is not False
                result['target_met'] = result['unanswered']==0 and result['invalid_replies']==0 and result['duplicate_replies']==0 and result['send_errors']==0 and result['actual_send_rps'] >= rate*.95 and result['latency_ms']['p95'] < 10 and result['database_integrity_ok']
                report['results'].append(result)
                (OUT/(name+'.json')).write_text(json.dumps(result,indent=2)+'\n')
                (OUT/'report.json').write_text(json.dumps(report,indent=2)+'\n')
                print(name, 'valid=',result['valid_replies'],'unanswered=',result['unanswered'],'p95_ms=',round(result['latency_ms']['p95'],2),'target_met=',result['target_met'],flush=True)
            finally:
                stop(server)
    print('Report:',OUT/'report.json',flush=True)

if __name__=='__main__':
    main()
