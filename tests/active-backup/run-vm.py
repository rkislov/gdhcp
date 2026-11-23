#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Two real Debian/kernel/systemd VMs. Docker only hosts QEMU and the L2 client.
No guest NIC is bridged to a host or production network. Fencing kills the whole
fixture VM and confirms process exit; no guest can power itself back on.
"""
import hashlib,http.server,json,os,shutil,socket,subprocess as sp,threading,time,urllib.request
from pathlib import Path
OUT=Path('/results');ROOT=Path('/tmp/pair-root');VIP='192.0.2.10'
VMS={};FENCES=[];DENY=False;REPORT={'results':[],'virtualization':'QEMU TCG, two independent Debian Linux kernels/systemd','production_hypervisors_tested':False}
def run(*args):
    try:return sp.check_output(args,text=True,stderr=sp.STDOUT).strip()
    except sp.CalledProcessError as e:
        print(e.output,flush=True);raise
def wait(fn,timeout=150):
    end=time.monotonic()+timeout
    while time.monotonic()<end:
        try:
            v=fn()
            if v:return v
        except (OSError,ValueError,AssertionError,sp.SubprocessError):pass
        time.sleep(.5)
    raise RuntimeError('VM condition timed out')
def command(n,*args,check=True):
    req=urllib.request.Request('http://127.0.0.1:'+str(18081+'ab'.index(n)),data=json.dumps(args).encode(),method='POST')
    with urllib.request.urlopen(req,timeout=50) as r:result=json.load(r)
    if check:assert result['code']==0,result
    return result['out'] if check else result
class Fencer(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        target=self.path.removeprefix('/fence/')
        if target not in VMS or DENY:self.send_response(503);self.end_headers();return
        vm=VMS[target]
        if vm.poll() is None:vm.kill();vm.wait(timeout=10)
        assert vm.poll() is not None
        FENCES.append({'node':target,'time':time.time(),'power_off_confirmed':True})
        self.send_response(200);self.end_headers()
    def log_message(self,*args):pass

def certs():
    p=Path('/tmp/pki');p.mkdir()
    run('openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(p/'ca.key'),'-out',str(p/'ca.pem'),'-days','2','-subj','/CN=VM fixture CA')
    for n in 'ab':
        run('openssl','req','-new','-newkey','rsa:2048','-nodes','-keyout',str(p/(n+'.key')),'-out',str(p/(n+'.csr')),'-subj','/CN='+n+'.test')
        (p/'ext').write_text('basicConstraints=CA:FALSE\nkeyUsage=digitalSignature\nextendedKeyUsage=serverAuth,clientAuth\nsubjectAltName=DNS:'+n+'.test,URI:spiffe://godhcp/cluster/vm/node/'+n+'\n')
        run('openssl','x509','-req','-in',str(p/(n+'.csr')),'-CA',str(p/'ca.pem'),'-CAkey',str(p/'ca.key'),'-CAcreateserial','-out',str(p/(n+'.pem')),'-days','2','-extfile',str(p/'ext'))
    return p

def setup():
    ROOT.mkdir();sp.run(['tar','-xpf','/input/rootfs.tar','-C',str(ROOT)],check=True)
    REPORT['binary_sha256']=hashlib.sha256((ROOT/'usr/bin/godhcp').read_bytes()).hexdigest()
    p=certs();(ROOT/'etc/machine-id').write_text('')
    (ROOT/'etc/systemd/system/pair-agent.service').write_text('''[Unit]
Before=godhcp.service
[Service]
Type=notify
NotifyAccess=all
ExecStart=/usr/bin/python3 /opt/pair-lab/agent.py
[Install]
WantedBy=multi-user.target
''')
    os.symlink('/etc/systemd/system/pair-agent.service',ROOT/'etc/systemd/system/multi-user.target.wants/pair-agent.service')
    drop=ROOT/'etc/systemd/system/godhcp.service.d';drop.mkdir(exist_ok=True)
    (drop/'pair.conf').write_text(Path('/source/deploy/cluster/systemd-override.conf.example').read_text()+'''\nRestart=no
StandardOutput=journal+console
StandardError=journal+console
[Unit]
Requires=pair-agent.service
After=pair-agent.service
''')
    (ROOT/'etc/godhcp/config.yml').write_text('''server:
  listen: ':67'
  server_id: 192.0.2.10
  authoritative: true
  lease_default: 120s
  lease_max: 120s
database:
  driver: sqlite
  dsn: /var/lib/godhcp/leases.db
api:
  listen: ':8181'
  auth:
    jwt_secret: '${JWT_SECRET}'
    api_keys:
      - {name: vm-fixture, key: vm-fixture-not-a-production-secret, role: admin}
web:
  enabled: false
dhcpv6:
  enabled: true
  preferred_seconds: 1800
  valid_seconds: 3600
  pools:
    - {interface: eth0, prefix: 'fd42:10::/64', start: 'fd42:10::100', end: 'fd42:10::110'}
    - {interface: eth0, prefix: 'fd42:20::/64', start: 'fd42:20::100', end: 'fd42:20::110', relays: ['fd42:10::2']}
subnets:
 - id: lab
   network: 192.0.2.0/24
   range: [192.0.2.100, 192.0.2.150]
   gateway: 192.0.2.10
   dns: [192.0.2.53]
   t1: 5s
   t2: 90s
logging:
  level: debug
''')
    uid=int(next(x for x in (ROOT/'etc/passwd').read_text().splitlines() if x.startswith('godhcp:')).split(':')[2])
    tlsdir=ROOT/'etc/godhcp/pair-tls';tlsdir.mkdir()
    for n in 'ab':
        (ROOT/'etc/pair-node').write_text(n)
        for old in tlsdir.iterdir():old.unlink()
        for name in ('ca.pem',n+'.pem',n+'.key'):shutil.copy(p/name,tlsdir/name);os.chown(tlsdir/name,uid,uid);(tlsdir/name).chmod(0o600)
        (ROOT/'etc/godhcp/cluster.yml').write_text(f'''mode: active-backup
primary: a
failover_timeout: 30s
id: vm
node: {n}
listen: 192.0.2.{11+'ab'.index(n)}:8444
data_dir: /var/lib/godhcp/pair
ca: /etc/godhcp/pair-tls/ca.pem
cert: /etc/godhcp/pair-tls/{n}.pem
key: /etc/godhcp/pair-tls/{n}.key
peers:
 - {{id: a, address: '192.0.2.11:8444', hostname: a.test}}
 - {{id: b, address: '192.0.2.12:8444', hostname: b.test}}
fence: [/usr/bin/python3, /opt/pair-lab/agent.py, fence]
ip_up: [/usr/bin/python3, /opt/pair-lab/ip-hook.py, up]
ip_down: [/usr/bin/python3, /opt/pair-lab/ip-hook.py, down]
''')
        os.chown(ROOT/'etc/godhcp/cluster.yml',uid,uid);(ROOT/'etc/godhcp/cluster.yml').chmod(0o600)
        image=Path('/tmp/'+n+'.ext4')
        with image.open('wb') as f:f.truncate(3*1024**3)
        run('mkfs.ext4','-q','-F','-d',str(ROOT),str(image))
    run('ip','link','add','br-pair','type','bridge');run('ip','link','set','br-pair','up')
    run('ip','addr','add','192.0.2.200/24','dev','br-pair')
    run('ip','-6','addr','add','fd42:10::2/64','dev','br-pair','nodad')
    run('ip','-6','addr','add','fe80::2/64','dev','br-pair','nodad')
    for n in 'ab':
        run('ip','tuntap','add','tap-'+n,'mode','tap');run('ip','link','set','tap-'+n,'master','br-pair');run('ip','link','set','tap-'+n,'up')
    threading.Thread(target=http.server.ThreadingHTTPServer(('0.0.0.0',19090),Fencer).serve_forever,daemon=True).start()

def start(n):
    if n in VMS:assert VMS[n].poll() is not None
    i='ab'.index(n)
    kernel=sorted((ROOT/'boot').glob('vmlinuz-*'))[-1];initrd=sorted((ROOT/'boot').glob('initrd.img-*'))[-1]
    args=['qemu-system-aarch64','-machine','virt,gic-version=3','-cpu','cortex-a72','-smp','2','-m','768','-accel','tcg,thread=multi','-nodefaults','-display','none','-monitor','none','-serial','stdio','-kernel',str(kernel),'-initrd',str(initrd),'-append','root=/dev/vda rw console=ttyAMA0 panic=1 net.ifnames=0','-drive','if=none,id=root,file=/tmp/'+n+'.ext4,format=raw','-device','virtio-blk-device,drive=root','-netdev','tap,id=service,ifname=tap-'+n+',script=no,downscript=no','-device','virtio-net-device,netdev=service,mac=52:54:00:00:01:0'+str(i+1),'-netdev','user,id=management,hostfwd=tcp:127.0.0.1:'+str(18081+i)+'-:9000','-device','virtio-net-device,netdev=management,mac=52:54:00:00:02:0'+str(i+1)]
    (OUT/(n+'-qemu.json')).write_text(json.dumps(args,indent=2))
    with (OUT/(n+'-console.log')).open('a') as log:VMS[n]=sp.Popen(args,stdout=log,stderr=sp.STDOUT)
    wait(lambda:command(n,'cat','/proc/sys/kernel/random/boot_id'))

def owns(n):
    if VMS[n].poll() is not None:return False
    return any(a.get('local')==VIP for link in json.loads(command(n,'ip','-j','addr','show','dev','eth0')) for a in link['addr_info'])
def active(n):
    return owns(n) and 'active-backup active' in command(n,'journalctl','-b','-u','godhcp','--no-pager')
def client(name,mode='dora',**extra):
    a=dict(name=name,mode=mode,iface='br-pair',network='192.0.2.0/24',gateway=VIP,server=VIP);a.update(extra)
    result=run('python3','/source/tests/integration/dhcp_lab.py','client',json.dumps(a))
    return json.loads(result)
def client6(mode):
    print(run('python3','/source/tests/active-backup/client6.py',mode),flush=True)
def api_config(value=None):
    request=urllib.request.Request('http://'+VIP+':8181/api/v1/config',data=None if value is None else json.dumps(value).encode(),headers={'Accept':'application/json','Content-Type':'application/json','X-API-Key':'vm-fixture-not-a-production-secret'},method='GET' if value is None else 'PUT')
    with urllib.request.urlopen(request,timeout=10) as response:return json.load(response)
def record(name,**details):
    REPORT['results'].append(dict(name=name,status='PASS',**details));print('PASS',name,flush=True)
def partition(n):
    command(n,'iptables','-I','INPUT','-p','tcp','--dport','8444','-j','DROP')
    command(n,'iptables','-I','OUTPUT','-p','tcp','--dport','8444','-j','DROP')
def main():
    global DENY
    setup();start('a');start('b');wait(lambda:active('a'))
    boots={n:command(n,'cat','/proc/sys/kernel/random/boot_id') for n in 'ab'}
    assert boots['a']!=boots['b'];assert not owns('b')
    record('two independent kernels, systemd, one active',boot_ids=boots,kernels={n:command(n,'uname','-a') for n in 'ab'},service_users={n:command(n,'systemctl','show','godhcp','-p','User','-p','AmbientCapabilities') for n in 'ab'})
    client6('bind');record('DHCPv6 direct and relay initial allocation')
    lease=client('pair-initial',mac='02:00:00:00:10:01');run('ip','addr','add',lease['ip']+'/24','dev','br-pair');record('DHCP DORA',lease=lease)
    command('a','tc','qdisc','add','dev','eth0','root','netem','delay','25ms','5ms','loss','0.1%')
    allocated={lease['ip']}
    retries=0
    for i in range(10):
        for attempt in range(3):
            try:
                result=client('pair-load-'+str(i),mac='02:00:00:01:00:'+format(i,'02x'))
                break
            except sp.CalledProcessError as e:
                if 'no DHCP reply' not in e.output or attempt==2:raise
                retries+=1
        assert result['ip'] not in allocated;allocated.add(result['ip'])
    stats=command('a','tc','-s','qdisc','show','dev','eth0')
    command('a','tc','qdisc','del','dev','eth0','root')
    assert active('a') and VMS['b'].poll() is None
    record('10 new clients under 25ms jitter and 0.1 percent loss',unique_addresses=len(allocated),retries=retries,netem=stats)
    before_yaml=command('a','sha256sum','/etc/godhcp/config.yml')
    policy=api_config();policy['subnets'][0]['domain']='qualified.test';api_config(policy)
    command('a','systemctl','reload','godhcp')
    assert api_config()['subnets'][0]['domain']=='qualified.test'
    assert command('a','sha256sum','/etc/godhcp/config.yml')==before_yaml
    record('API configuration commit and systemd reload preserve replicated policy')
    seconds=int(os.environ.get('GODHCP_PAIR_SOAK_SECONDS','0'))
    if seconds:
        started=time.monotonic();rounds=0
        def resources(n):
            return json.loads(command(n,'python3','-c',"import os,json,subprocess; p=subprocess.check_output(['systemctl','show','godhcp','-p','MainPID','--value'],text=True).strip(); print(json.dumps({'fds':len(os.listdir('/proc/'+p+'/fd')),'rss_kib':int(subprocess.check_output(['ps','-o','rss=','-p',p],text=True))}))"))
        baseline={n:resources(n) for n in 'ab'}
        while time.monotonic()-started<seconds:
            client('pair-soak-renew',mode='renew',broadcast=False,**lease)
            client6('renew');rounds+=1
            assert active('a') and VMS['b'].poll() is None and not owns('b')
            time.sleep(.5)
        after={n:resources(n) for n in 'ab'}
        for n in 'ab':assert after[n]['fds']<=baseline[n]['fds']+8,(baseline,after)
        record('continuous IPv4 and IPv6 renewals with stable role',seconds=time.monotonic()-started,rounds=rounds,before=baseline,after=after)
    failure_started=time.monotonic()
    command('a','systemctl','kill','--kill-who=main','--signal=SIGSTOP','godhcp')
    wait(lambda:active('b'))
    assert api_config()['subnets'][0]['domain']=='qualified.test'
    record('hung active daemon is fenced with full VM shutdown',recovery_seconds=time.monotonic()-failure_started)

    client('pair-renew-after-poweroff',mode='renew',broadcast=False,**lease)
    second=client('pair-new-on-backup',mac='02:00:00:00:10:02');assert second['ip']!=lease['ip'];run('ip','addr','add',second['ip']+'/24','dev','br-pair')
    client6('renew');record('hung active VM fencing, VIP takeover, renew and new lease')
    start('a');wait(lambda:'replica synchronized' in command('b','journalctl','-b','-u','godhcp','--no-pager'));assert VMS['a'].poll() is None and active('b') and not owns('a')
    record('stale primary rejoins without failback')
    VMS['b'].kill();VMS['b'].wait();wait(lambda:active('a'))
    client('pair-second-failover',mode='renew',broadcast=False,**second)
    client6('renew');record('second power loss, replicated IPv4/IPv6 and DUID survive')
    start('b');wait(lambda:'replica synchronized' in command('a','journalctl','-b','-u','godhcp','--no-pager'));assert VMS['b'].poll() is None
    # Drop only replication responses: the backup can persist a PUT whose ACK is lost.
    command('b','iptables','-I','OUTPUT','-p','tcp','--sport','8444','-j','DROP')
    wait(lambda:VMS['b'].poll() is not None)
    assert active('a');client('pair-partition-renew',mode='renew',broadcast=False,**second)
    client6('renew');record('lost replication ACK fences backup before degraded service')
    # A rebooted lone survivor cannot assume its old role from disk.
    old=command('a','cat','/proc/sys/kernel/random/boot_id')
    command('a','systemctl','reboot',check=False)
    wait(lambda:command('a','cat','/proc/sys/kernel/random/boot_id')!=old)
    time.sleep(5);assert not owns('a');record('lone restart waits for synchronization')
    start('b');wait(lambda:active('a'));client('pair-after-reboot',mode='renew',broadcast=False,**second)
    client6('renew');record('systemd starts after reboot, durable IPv4/IPv6 state recovers')
    DENY=True;partition('b')
    wait(lambda:not owns('a') and not owns('b'),timeout=90)
    wait(lambda:all(command(n,'systemctl','is-active','godhcp',check=False)['code']!=0 for n in 'ab'),timeout=90)
    assert not owns('a') and not owns('b')
    from scapy.all import Ether,IP,UDP,BOOTP,DHCP,srp
    packet=Ether(dst='ff:ff:ff:ff:ff:ff')/IP(src='0.0.0.0',dst='255.255.255.255')/UDP(sport=68,dport=67)/BOOTP(chaddr=bytes.fromhex('020000001099'),xid=123456,flags=0x8000)/DHCP(options=[('message-type','discover'),'end'])
    answered,_=srp(packet,iface='br-pair',timeout=4,verbose=False);assert not answered
    client6('silent');record('unavailable fencing blocks activation and IPv4/IPv6 replies')
    REPORT['fencing']=FENCES
try:
    main()
except Exception as e:
    REPORT['error']=repr(e);raise
finally:
    for n,vm in VMS.items():
        if vm.poll() is None:
            try:(OUT/(n+'-journal.log')).write_text(command(n,'journalctl','-b','-u','godhcp','--no-pager'))
            except Exception:pass
            vm.kill();vm.wait()
    REPORT['fencing']=FENCES
    (OUT/'report.json').write_text(json.dumps(REPORT,indent=2)+'\n')
