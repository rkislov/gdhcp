#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Three real Debian/kernel/systemd VMs running Raft. Docker only hosts QEMU and the L2 client.
No guest NIC is bridged to a host or production network. Fencing kills the whole
fixture VM and confirms process exit; no guest can power itself back on.
"""
import hashlib,http.server,json,os,shutil,socket,subprocess as sp,threading,time,urllib.request
from pathlib import Path
OUT=Path('/results');ROOT=Path('/tmp/pair-root');VIP='192.0.2.10'
VMS={};FENCES=[];DENY=False;REPORT={'started_utc':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'results':[],'virtualization':'QEMU TCG, three independent Debian Linux kernels/systemd','production_hypervisors_tested':False}
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
    req=urllib.request.Request('http://127.0.0.1:'+str(18081+'abc'.index(n)),data=json.dumps(args).encode(),method='POST')
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
    for n in 'abc':
        run('openssl','req','-new','-newkey','rsa:2048','-nodes','-keyout',str(p/(n+'.key')),'-out',str(p/(n+'.csr')),'-subj','/CN='+n+'.test')
        (p/'ext').write_text('basicConstraints=CA:FALSE\nkeyUsage=digitalSignature\nextendedKeyUsage=serverAuth,clientAuth\nsubjectAltName=DNS:'+n+'.test,URI:spiffe://godhcp/cluster/vm/node/'+n+'\n')
        run('openssl','x509','-req','-in',str(p/(n+'.csr')),'-CA',str(p/'ca.pem'),'-CAkey',str(p/'ca.key'),'-CAcreateserial','-out',str(p/(n+'.pem')),'-days','2','-extfile',str(p/'ext'))
    return p

def setup():
    ROOT.mkdir();sp.run(['tar','-xpf','/input/rootfs.tar','-C',str(ROOT)],check=True)
    REPORT['harness_sha256']=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
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
  lease_default: 3600s
  lease_max: 3600s
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
    for n in 'abc':
        (ROOT/'etc/pair-node').write_text(n)
        for old in tlsdir.iterdir():old.unlink()
        for name in ('ca.pem',n+'.pem',n+'.key'):shutil.copy(p/name,tlsdir/name);os.chown(tlsdir/name,uid,uid);(tlsdir/name).chmod(0o600)
        (ROOT/'etc/godhcp/cluster.yml').write_text(f'''mode: raft
bootstrap: {'true' if n=='a' else 'false'}
id: vm
node: {n}
listen: 192.0.2.{11+'abc'.index(n)}:8444
data_dir: /var/lib/godhcp/pair
ca: /etc/godhcp/pair-tls/ca.pem
cert: /etc/godhcp/pair-tls/{n}.pem
key: /etc/godhcp/pair-tls/{n}.key
peers:
 - {{id: a, address: '192.0.2.11:8444', hostname: a.test}}
 - {{id: b, address: '192.0.2.12:8444', hostname: b.test}}
 - {{id: c, address: '192.0.2.13:8444', hostname: c.test}}
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
    for n in 'abc':
        run('ip','tuntap','add','tap-'+n,'mode','tap');run('ip','link','set','tap-'+n,'master','br-pair');run('ip','link','set','tap-'+n,'up')
    threading.Thread(target=http.server.ThreadingHTTPServer(('0.0.0.0',19090),Fencer).serve_forever,daemon=True).start()

def start(n):
    if n in VMS:assert VMS[n].poll() is not None
    i='abc'.index(n)
    kernel=sorted((ROOT/'boot').glob('vmlinuz-*'))[-1];initrd=sorted((ROOT/'boot').glob('initrd.img-*'))[-1]
    args=['qemu-system-aarch64','-machine','virt,gic-version=3','-cpu','cortex-a72','-smp','2','-m','768','-accel','tcg,thread=multi','-nodefaults','-display','none','-monitor','none','-serial','stdio','-kernel',str(kernel),'-initrd',str(initrd),'-append','root=/dev/vda rw console=ttyAMA0 panic=1 net.ifnames=0','-drive','if=none,id=root,file=/tmp/'+n+'.ext4,format=raw','-device','virtio-blk-device,drive=root','-netdev','tap,id=service,ifname=tap-'+n+',script=no,downscript=no','-device','virtio-net-device,netdev=service,mac=52:54:00:00:01:0'+str(i+1),'-netdev','user,id=management,hostfwd=tcp:127.0.0.1:'+str(18081+i)+'-:9000','-device','virtio-net-device,netdev=management,mac=52:54:00:00:02:0'+str(i+1)]
    (OUT/(n+'-qemu.json')).write_text(json.dumps(args,indent=2))
    with (OUT/(n+'-console.log')).open('a' if n in VMS else 'w') as log:VMS[n]=sp.Popen(args,stdout=log,stderr=sp.STDOUT)
    wait(lambda:VMS[n].poll() is not None or command(n,'cat','/proc/sys/kernel/random/boot_id'))

def owns(n):
    if VMS[n].poll() is not None:return False
    return any(a.get('local')==VIP for link in json.loads(command(n,'ip','-j','addr','show','dev','eth0')) for a in link['addr_info'])
def active(n):
    return owns(n) and command(n,'python3','-c',"import urllib.request; urllib.request.urlopen('http://127.0.0.1:8181/readyz',timeout=1).read()",check=False)['code']==0
def client(name,mode='dora',**extra):
    a=dict(name=name,mode=mode,iface='br-pair',network='192.0.2.0/24',gateway=VIP,server=VIP,lease_time=3600);a.update(extra)
    result=run('python3','/source/tests/integration/dhcp_lab.py','client',json.dumps(a))
    return json.loads(result)
def client6(mode):
    print(run('python3','/source/tests/active-backup/client6.py',mode),flush=True)
def api_config(value=None):
    request=urllib.request.Request('http://'+VIP+':8181/api/v1/config',data=None if value is None else json.dumps(value).encode(),headers={'Accept':'application/json','Content-Type':'application/json','X-API-Key':'vm-fixture-not-a-production-secret'},method='GET' if value is None else 'PUT')
    with urllib.request.urlopen(request,timeout=10) as response:return json.load(response)
def api_lease(address):
    request=urllib.request.Request('http://'+VIP+':8181/api/v1/leases/'+address,headers={'X-API-Key':'vm-fixture-not-a-production-secret'})
    with urllib.request.urlopen(request,timeout=10) as response:return json.load(response)
def record(name,**details):
    REPORT['results'].append(dict(name=name,status='PASS',observed_at=time.time(),**details));print('PASS',name,flush=True)
def partition(n):
    command(n,'iptables','-I','INPUT','-p','tcp','--dport','8444','-j','DROP')
    command(n,'iptables','-I','OUTPUT','-p','tcp','--dport','8444','-j','DROP')

def owner():
    owners=[n for n in 'abc' if owns(n)]
    if len(owners)>1:raise RuntimeError('multiple VIP owners: '+repr(owners))
    return owners[0] if owners and active(owners[0]) else False

def revive_all():
    for n in 'abc':
        if VMS[n].poll() is not None:start(n)
    leader=wait(owner)
    # A cold-start election may fence the persisted previous owner while booting.
    for n in 'abc':
        if VMS[n].poll() is not None:start(n)
    wait(lambda:all(VMS[n].poll() is None and command(n,'systemctl','is-active','godhcp',check=False)['code']==0 for n in 'abc'))
    return wait(owner)

def silence():
    from scapy.all import Ether,IP,UDP,BOOTP,DHCP,srp
    packet=Ether(dst='ff:ff:ff:ff:ff:ff')/IP(src='0.0.0.0',dst='255.255.255.255')/UDP(sport=68,dport=67)/BOOTP(chaddr=bytes.fromhex('020000001099'),xid=123456,flags=0x8000)/DHCP(options=[('message-type','discover'),'end'])
    answered,_=srp(packet,iface='br-pair',timeout=4,verbose=False)
    assert not answered, 'DHCPv4 answered without authority'
    client6('silent')

def renew(lease):
    result=client('raft-vm-renew',mode='renew',broadcast=False,**lease)
    assert result['ip']==lease['ip']
    client6('renew')
    assert api_config()['subnets'][0]['domain']=='raft-vm.test'

def main():
    global DENY,SNIFFER
    from scapy.all import AsyncSniffer
    setup()
    SNIFFER=AsyncSniffer(iface='br-pair',filter='arp or port 67 or port 68 or port 546 or port 547 or tcp port 8444',store=True)
    SNIFFER.start()
    for n in 'abc':start(n)
    leader=wait(owner)
    boots={n:command(n,'cat','/proc/sys/kernel/random/boot_id') for n in 'abc'}
    assert len(set(boots.values()))==3
    record('three independent kernels and systemd, single active owner',leader=leader,boot_ids=boots,kernels={n:command(n,'uname','-a') for n in 'abc'},service_users={n:command(n,'systemctl','show','godhcp','-p','User','-p','AmbientCapabilities') for n in 'abc'})
    # Bootstrap is for the first creation only, including on the fixture's A disk.
    command('a','sed','-i','s/bootstrap: true/bootstrap: false/','/etc/godhcp/cluster.yml')
    lease=client('raft-vm-initial',mac='02:00:00:00:30:01')
    run('ip','addr','add',lease['ip']+'/24','dev','br-pair')
    client6('bind');record('DHCPv4 DORA and DHCPv6 direct and relay',lease=lease)
    policy=api_config();policy['subnets'][0]['domain']='raft-vm.test';api_config(policy)
    command(leader,'systemctl','reload','godhcp')
    assert api_config()['subnets'][0]['domain']=='raft-vm.test'
    record('API policy replicated and systemd reload succeeds')
    began=time.monotonic();rounds=0
    while time.monotonic()-began<30:
        renew(lease);assert owner()==leader;rounds+=1
    record('continuous IPv4 and IPv6 renewals with stable owner',seconds=time.monotonic()-began,rounds=rounds)

    began=time.monotonic()
    command(leader,'systemctl','kill','--kill-who=main','--signal=SIGSTOP','godhcp')
    successor=wait(lambda:(n:=owner()) and n!=leader and n)
    assert VMS[leader].poll() is not None
    assert any(f['node']==leader and f['power_off_confirmed'] for f in FENCES)
    renew(lease)
    record('hung active fenced as whole VM, VIP and leases survive',old=leader,new=successor,recovery_seconds=time.monotonic()-began)
    start(leader);revive_all()
    for _ in range(3):renew(lease);assert owner()==successor
    record('fenced VM rejoins from original disk without failback')

    leader=owner();began=time.monotonic();partition(leader)
    successor=wait(lambda:(n:=owner()) and n!=leader and n)
    assert VMS[leader].poll() is not None
    renew(lease)
    record('live active peer-network partition fenced before takeover',old=leader,new=successor,recovery_seconds=time.monotonic()-began)
    start(leader);revive_all()
    leader=owner()
    for n in 'abc':
        if n!=leader:VMS[n].kill();VMS[n].wait()
    wait(lambda:not owns(leader))
    silence();record('loss of two replicas withdraws VIP and silences IPv4 and IPv6',survivor=leader)
    leader=revive_all();renew(lease)
    record('quorum restoration recovers original leases and API policy',leader=leader)

    persisted=api_lease(lease['ip'])
    before={n:command(n,'cat','/proc/sys/kernel/random/boot_id') for n in 'abc'}
    for vm in VMS.values():vm.kill();vm.wait()
    leader=revive_all()
    assert api_lease(lease['ip'])==persisted, 'lease changed or disappeared before first renewal after cold restart'
    renew(lease)
    after={n:command(n,'cat','/proc/sys/kernel/random/boot_id') for n in 'abc'}
    assert all(before[n]!=after[n] for n in 'abc')
    record('cold restart of all three VMs restores systemd and durable IPv4 IPv6 DUID policy',boot_ids=after,leader=leader,lease_before_first_renewal=persisted)
    second=client('raft-vm-second',mac='02:00:00:00:30:02')
    assert second['ip']!=lease['ip']
    record('new allocation after recovery does not reuse existing lease',original=lease['ip'],new=second['ip'])

    leader=owner();DENY=True;partition(leader)
    wait(lambda:not any(owns(n) for n in 'abc'))
    wait(lambda:any('cluster activation blocked' in command(n,'journalctl','-b','-u','godhcp','--no-pager') for n in 'abc' if n!=leader))
    silence()
    assert VMS[leader].poll() is None
    record('majority cannot activate when fencing unavailable, both protocols silent')
    DENY=False
    successor=wait(lambda:(n:=owner()) and n!=leader and n)
    assert VMS[leader].poll() is not None
    renew(lease)
    record('restored fencing permits safe activation and renewal',old=leader,new=successor)
    packets=capture()
    from scapy.all import BOOTP,DHCP,Ether
    senders={}
    for packet in packets:
        if BOOTP in packet and DHCP in packet and Ether in packet and packet[BOOTP].op==2:
            options=dict(x for x in packet[DHCP].options if isinstance(x,tuple) and len(x)==2)
            key=(packet[BOOTP].xid,str(options.get('message-type')),bytes(packet[BOOTP].chaddr).hex())
            senders.setdefault(key,set()).add(packet[Ether].src)
    assert senders, 'capture contains no DHCP replies'
    duplicate={str(k):sorted(v) for k,v in senders.items() if len(v)>1}
    assert not duplicate, 'multiple VM senders for the same DHCP exchange: '+repr(duplicate)
    record('packet capture has one server MAC per DHCPv4 transaction and reply type',transactions=len(senders))
    REPORT['status']='PASS'

def capture():
    from scapy.all import wrpcap
    packets=SNIFFER.stop()
    wrpcap(str(OUT/'cluster.pcap'),packets)
    REPORT['captured_packets']=len(packets)
    return packets

SNIFFER=None
try:
    main()
except Exception as e:
    REPORT['status']='FAIL';REPORT['error']=repr(e);raise
finally:
    for n,vm in VMS.items():
        if vm.poll() is None:
            try:(OUT/(n+'-journal.log')).write_text(command(n,'journalctl','-b','-u','godhcp','--no-pager'))
            except Exception:pass
            vm.kill();vm.wait()
    if SNIFFER is not None and SNIFFER.running:
        capture()
    REPORT['fencing']=FENCES
    REPORT['finished_utc']=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())
    (OUT/'report.json').write_text(json.dumps(REPORT,indent=2)+'\n')
