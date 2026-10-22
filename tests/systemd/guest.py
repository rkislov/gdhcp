#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

"""Runs under real systemd/PID 1 in an isolated full Linux virtual machine."""
import json
import os
import traceback
from pathlib import Path
import pwd
import sqlite3
import subprocess as sp
import sys
import time
import urllib.request

STATE = Path('/var/lib/godhcp-lab')
STATE.mkdir(exist_ok=True)
RESULTS = Path('/results')
RESULTS.mkdir(exist_ok=True)
CONFIG = Path('/etc/godhcp/config.yml')


def run(*args, check=True):
    p = sp.run(args, text=True, stdout=sp.PIPE, stderr=sp.PIPE)
    if check and p.returncode:
        raise AssertionError(' '.join(args) + ': ' + p.stdout + p.stderr)
    return p


def output(*args):
    return run(*args).stdout.strip()


def network():
    run('ip','netns','add','access')
    run('ip','link','add','access0','type','veth','peer','name','client0')
    run('ip','link','set','client0','netns','access')
    run('ip','link','set','access0','up')
    run('ip','addr','add','198.18.10.1/24','dev','access0')
    for args in [('link','set','client0','name','eth0'),('link','set','eth0','address','02:00:00:aa:10:01'),('link','set','eth0','up'),('link','set','lo','up')]:
        run('ip','netns','exec','access','ip',*args)


def ready():
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        try:
            if output('systemctl','is-active','godhcp') == 'active':
                with urllib.request.urlopen('http://127.0.0.1:8080/readyz',timeout=1) as r:
                    if r.status == 200:
                        return
        except Exception:
            pass
        time.sleep(.2)
    raise AssertionError('godhcp did not become ready')


def client(name, **kw):
    a = {'iface':'eth0','mode':'dora','name':name,'network':'198.18.10.0/24','gateway':'198.18.10.1', **kw}
    return json.loads(output('ip','netns','exec','access','python3','/opt/godhcp-lab/dhcp_lab.py','client',json.dumps(a)))


def leases():
    with sqlite3.connect('/var/lib/godhcp/leases.db') as db:
        db.row_factory=sqlite3.Row
        return {r['ip']:dict(r) for r in db.execute('select * from leases')}


def main():
    phase = 2 if (STATE/'checkpoint.json').exists() else 1
    reportfile=STATE/'report.json'
    report=json.loads(reportfile.read_text()) if reportfile.exists() else {'os':Path('/etc/os-release').read_text(),'kernel':output('uname','-r'),'version':output('godhcp','version'),'results':[]}
    def check(name, fn):
        try:
            value=fn()
            report['results'].append({'name':name,'status':'PASS','boot':phase})
            print('PASS',name,flush=True)
            return value
        except Exception as e:
            report['results'].append({'name':name,'status':'FAIL','boot':phase,'error':str(e)})
            print('FAIL',name,str(e),flush=True)
        finally:
            reportfile.write_text(json.dumps(report,indent=2)+'\n')
    def properties():
        assert Path('/proc/1/comm').read_text().strip()=='systemd'
        assert output('systemctl','is-enabled','godhcp')=='enabled'
        ready()
        pid=output('systemctl','show','godhcp','-p','MainPID','--value')
        data=dict(line.split(':',1) for line in Path('/proc/'+pid+'/status').read_text().splitlines() if ':' in line)
        uid=pwd.getpwnam('godhcp').pw_uid
        assert uid != 0 and all(int(v)==uid for v in data['Uid'].split())
        for key in ('CapEff','CapPrm','CapBnd','CapAmb'):
            assert int(data[key],16)==0x400, (key,data[key])
        assert data['NoNewPrivs'].strip()=='1'
        assert Path('/proc/sys/net/ipv4/ip_unprivileged_port_start').read_text().strip()=='1024'
        sockets=output('ss','-ulpn')
        assert ':67 ' in sockets and 'godhcp' in sockets
        assert Path('/etc/godhcp/godhcp.env').stat().st_mode & 0o777==0o600
        assert run('runuser','-u','godhcp','--','cat','/etc/godhcp/godhcp.env',check=False).returncode != 0
        assert run('runuser','-u','nobody','--','cat','/etc/godhcp/config.yml',check=False).returncode != 0
        db=Path('/var/lib/godhcp/leases.db')
        assert db.stat().st_uid==uid and db.stat().st_mode & 0o007==0
        return pid
    check('systemd PID 1; enabled non-root service; bind capability; secret and database permissions', properties)
    if phase==1:
        initial=check('real DHCP DORA under packaged systemd unit',lambda:client('initial'))
        original=CONFIG.read_text()
        def valid_reload():
            pid=output('systemctl','show','godhcp','-p','MainPID','--value')
            CONFIG.write_text(original.replace('192.0.2.53','192.0.2.54'))
            run('systemctl','reload','godhcp')
            deadline=time.monotonic()+5
            while time.monotonic()<deadline:
                text=output('journalctl','-u','godhcp','--no-pager')
                if 'configuration reloaded' in text:
                    break
                time.sleep(.1)
            assert output('systemctl','show','godhcp','-p','MainPID','--value')==pid
            client('after-reload',dns='192.0.2.54')
            assert initial and leases()[initial['ip']]['state']=='bound'
        check('valid reload changes DHCP DNS without restart or loss of lease',valid_reload)
        def invalid_reload():
            CONFIG.write_text('server: [broken\n')
            result=run('systemctl','reload','godhcp',check=False)
            assert result.returncode != 0, 'systemctl reload reported success for invalid YAML'
            ready()
            client('after-invalid-reload',dns='192.0.2.54')
        check('invalid reload fails visibly and keeps previous working configuration',invalid_reload)
        CONFIG.write_text(original)
        run('systemctl','reload','godhcp',check=False)
        time.sleep(.5)
        def port_conflict():
            run('systemctl','stop','godhcp')
            p=sp.Popen(['python3','-c',"import socket,time;s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('0.0.0.0',67));print('bound',flush=True);time.sleep(30)"],stdout=sp.PIPE,text=True)
            try:
                assert p.stdout.readline().strip()=='bound'
                run('systemctl','start','godhcp',check=False)
                time.sleep(1)
                state=run('systemctl','is-active','godhcp',check=False).stdout.strip()
                assert state!='active', 'service stays active with unusable DHCP port'
            finally:
                p.terminate();p.wait()
                run('systemctl','reset-failed','godhcp',check=False)
                run('systemctl','restart','godhcp')
                ready()
        check('occupied UDP 67 causes startup failure instead of false active status',port_conflict)
        def restart():
            before=leases()
            run('systemctl','restart','godhcp');ready()
            assert leases()==before, 'restart changed leases'
            assert initial
            client('after-service-restart',mode='renew',broadcast=False,**initial)
        # Configure the client's acquired address for real kernel unicast ACK delivery.
        if initial:
            run('ip','netns','exec','access','ip','addr','add',initial['ip']+'/24','dev','eth0')
        check('systemctl restart preserves SQLite and original client renewal',restart)
        checkpoint={'boot_id':Path('/proc/sys/kernel/random/boot_id').read_text().strip(),'leases':leases(),'client':initial}
        with open(STATE/'checkpoint.json','w') as f:
            json.dump(checkpoint,f,indent=2);f.flush();os.fsync(f.fileno())
        (STATE/'boot1.journal').write_text(output('journalctl','-b','--no-pager'))
        print('LAB_REAL_KERNEL_REBOOT',checkpoint['boot_id'],flush=True)
        run('sync')
        run('systemctl','reboot')
    else:
        checkpoint=json.loads((STATE/'checkpoint.json').read_text())
        def reboot():
            current=Path('/proc/sys/kernel/random/boot_id').read_text().strip()
            assert current != checkpoint['boot_id'], 'kernel did not reboot'
            report['boot_ids']=[checkpoint['boot_id'],current]
            ready()
            assert leases()==checkpoint['leases'], 'leases changed across kernel reboot'
            initial=checkpoint['client'];assert initial
            run('ip','netns','exec','access','ip','addr','add',initial['ip']+'/24','dev','eth0')
            old_expiry=leases()[initial['ip']]['expires_at']
            client('after-kernel-reboot',mode='renew',broadcast=False,**initial)
            assert leases()[initial['ip']]['expires_at']>old_expiry
            new=client('new-after-reboot',mac='02:00:00:aa:10:02')
            assert new['ip'] not in checkpoint['leases'], 'occupied address allocated after boot'
        check('actual kernel reboot autostarts service, preserves leases and prevents duplicates',reboot)
        (STATE/'boot2.journal').write_text(output('journalctl','-b','--no-pager'))
        (STATE/'unit.txt').write_text(output('systemctl','cat','godhcp'))
        (STATE/'properties.txt').write_text(output('systemctl','show','godhcp'))
        reportfile.write_text(json.dumps(report,indent=2)+'\n')
        print('LAB_FINAL_REPORT '+json.dumps(report),flush=True)
        run('sync')
        run('systemctl','poweroff')


if __name__=='__main__':
    if len(sys.argv)>1 and sys.argv[1]=='network':
        network()
    else:
        try:
            main()
        except Exception as e:
            traceback.print_exc()
            print('LAB_FATAL '+repr(e),flush=True)
            run('systemctl','poweroff',check=False)
            raise
