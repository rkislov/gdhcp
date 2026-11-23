#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Disposable Docker-only netns fixture. Fencing here kills fixture PIDs only."""
import json
import os
from pathlib import Path
import signal
import subprocess as sp
import sys
import time

VIP='192.0.2.10'
VIP6='fd42:10::1'
RESULTS=Path('/results')
def run(*a):
 try:return sp.check_output(a,text=True,stderr=sp.PIPE).strip()
 except sp.CalledProcessError as e:
  print(e.stderr,file=sys.stderr)
  raise
def ip(*a):return run('ip',*a)
def owns(n,address=VIP):
 return any(a.get('local')==address for link in json.loads(ip('-n',n,'-j','addr','show','dev','eth0')) for a in link['addr_info'])
def hook(action,node):
 if action=='fence':
  if Path('/tmp/deny-fence').exists():raise RuntimeError('injected fencing failure')
  assert node in 'ab' and len(node)==1, 'fixture fencing accepts only a/b/c'
  inode=os.stat('/run/netns/'+node).st_ino
  # Fence all workload descendants, including helper process groups. A PID-only
  # kill would leave a running nsupdate or IP hook able to affect the network.
  def members():
   out=[]
   for proc in Path('/proc').iterdir():
    if not proc.name.isdigit():continue
    try:
     if os.stat(proc/'ns/net').st_ino==inode:out.append(int(proc.name))
    except (FileNotFoundError,ProcessLookupError,PermissionError):pass
   return out
  for _ in range(100):
   targets=members()
   if not targets:break
   for sig in (signal.SIGSTOP,signal.SIGKILL):
    for pid in targets:
     try:os.kill(pid,sig)
     except ProcessLookupError:pass
   time.sleep(.02)
  else:raise RuntimeError('fencing did not empty the target namespace')
  if owns(node):ip('-n',node,'addr','del',VIP+'/24','dev','eth0')
  if owns(node,VIP6):ip('-n',node,'addr','del',VIP6+'/64','dev','eth0')
 elif action=='down':
  if owns(node):ip('-n',node,'addr','del',VIP+'/24','dev','eth0')
  if owns(node,VIP6):ip('-n',node,'addr','del',VIP6+'/64','dev','eth0')
 elif action=='up':
  run('ip','netns','exec',node,'python3','/lab/cluster_ip.py','up','eth0',VIP+'/24')
  run('ip','netns','exec',node,'python3','/lab/cluster_ip.py','up','eth0',VIP6+'/64')
 else:raise RuntimeError(action)
def wait(fn,seconds=60):
 end=time.monotonic()+seconds
 while time.monotonic()<end:
  value=fn()
  if value:return value
  time.sleep(.2)
 raise RuntimeError('condition timed out')
def client(mode,**extra):
 args=dict(iface='eth0',mode=mode,name='cluster-'+mode,network='192.0.2.0/24',gateway=VIP,server=VIP,renewal_time=60,**extra)
 out=run('ip','netns','exec','client','python3','/lab/dhcp_lab.py','client',json.dumps(args))
 return json.loads(out)
def main():
 RESULTS.mkdir(exist_ok=True)
 (RESULTS/'report.json').unlink(missing_ok=True)
 ip('link','add','br-test','type','bridge');ip('link','set','br-test','up')
 for i,n in enumerate(['a','b','client']):
  ip('netns','add',n);ip('-n',n,'link','set','lo','up')
  ip('link','add','v-'+n,'type','veth','peer','name','p-'+n)
  ip('link','set','p-'+n,'netns',n);ip('-n',n,'link','set','p-'+n,'name','eth0')
  ip('link','set','v-'+n,'master','br-test');ip('link','set','v-'+n,'up');ip('-n',n,'link','set','eth0','up')
  if n!='client':ip('-n',n,'addr','add',f'192.0.2.{11+i}/24','dev','eth0')
  ip('-n',n,'addr','add',f'fd42:10::{11+i if n!="client" else 2}/64','dev','eth0','nodad')
  ip('-n',n,'addr','add',f'fe80::{11+i if n!="client" else 2}/64','dev','eth0','nodad')
 peers=[dict(id=n,address=f'192.0.2.{11+i}:8444',hostname=n+'.test') for i,n in enumerate('ab')]
 processes={}
 samba=None
 samba_enabled=os.environ.get('GODHCP_CLUSTER_SAMBA')=='1'
 try:
  if samba_enabled:
   ip('addr','add','192.0.2.53/24','dev','br-test')
   with open('/etc/hosts','a') as hosts:hosts.write('\n192.0.2.53 dc.dhcp.test dc\n')
   log=open(RESULTS/'samba-setup.log','w')
   env=dict(os.environ,GODHCP_TEST_DC_IP='192.0.2.53')
   samba=sp.Popen(['/bin/bash','/setup-samba.sh'],env=env,stdout=log,stderr=sp.STDOUT,start_new_session=True);log.close()
   wait(lambda:Path('/tmp/godhcp-dc-ready').exists(),120)
  def dns(name,kind):return run('dig','@192.0.2.53',name,kind,'+short')

  for i,n in enumerate('ab'):
   cfg={'server':{'listen':':67','server_id':VIP,'authoritative':True,'lease_default':'120s','lease_max':'120s'},'api':{'listen':':8181'},'metrics':{'enabled':False},'subnets':[{'id':'lan','network':'192.0.2.0/24','range':['192.0.2.150','192.0.2.160'],'gateway':VIP}]}
   cfg['dhcpv6']=dict(enabled=True,preferred_seconds=60,valid_seconds=120,pools=[dict(interface='eth0',prefix='fd42:10::/64',start='fd42:10::100',end='fd42:10::110'),dict(interface='eth0',prefix='fd42:20::/64',start='fd42:20::100',end='fd42:20::110',relays=['fd42:10::2'])])
   if samba_enabled:cfg['ddns']=dict(enabled=True,server='dc.dhcp.test',zone='hosts.dhcp.test',reverse_zones=['2.0.192.in-addr.arpa'],principal='dhcp-dns@DHCP.TEST',keytab='/tmp/dhcp-dns.keytab',interval_seconds=1)
   manifest=dict(id='lab',node=n,listen=peers[i]['address'],data_dir='/tmp/raft-'+n,mode='active-backup',primary='a',failover_timeout='30s',ca='/pki/ca.pem',cert=f'/pki/{n}.pem',key=f'/pki/{n}.key',peers=peers,fence=['/usr/bin/python3','/lab/cluster_lab.py','hook','fence'],ip_up=['/usr/bin/python3','/lab/cluster_lab.py','hook','up',n],ip_down=['/usr/bin/python3','/lab/cluster_lab.py','hook','down',n])
   Path('/tmp/'+n+'.json').write_text(json.dumps(cfg));Path('/tmp/'+n+'-cluster.json').write_text(json.dumps(manifest))
   log=open(RESULTS/(n+'.log'),'w')
   p=sp.Popen(['ip','netns','exec',n,'godhcp','-config','/tmp/'+n+'.json','-cluster-config','/tmp/'+n+'-cluster.json'],stdout=log,stderr=sp.STDOUT);log.close();processes[n]=p;Path('/tmp/'+n+'.pid').write_text(str(p.pid))
  def active():
   owners=[n for n in 'ab' if owns(n)]
   if len(owners)!=1:return False
   n=owners[0]
   try:
    sp.check_output(['ip','netns','exec',n,'python3','-c',"import urllib.request;urllib.request.urlopen('http://127.0.0.1:8181/readyz',timeout=1).read()"],stderr=sp.PIPE)
    return n
   except sp.CalledProcessError:return False
  leader=wait(active);print('PASS elected active daemon',leader,flush=True)
  lease=client('dora');print('PASS DHCP DORA',lease,flush=True)
  ip('-n','client','addr','add',lease['ip']+'/24','dev','eth0')
  if samba_enabled:
   wait(lambda:dns('cluster-dora.hosts.dhcp.test','A')==lease['ip'])
   wait(lambda:dns('150.2.0.192.in-addr.arpa','PTR')=='cluster-dora.hosts.dhcp.test.')
   owner_before=dns('_godhcp-owner.cluster-dora.hosts.dhcp.test','TXT');assert owner_before
   print('PASS Samba A/PTR before failover',flush=True)
  print(run('ip','netns','exec','client','python3','/lab/cluster6_wire.py','bind'),flush=True)
  # Two-member takeover after actual process loss; VM power loss is a separate suite.
  processes[leader].kill();processes[leader].wait()
  successor=wait(lambda:(n:=active()) and n!=leader and n)
  print('PASS fenced failed daemon and moved VIP',leader,successor,flush=True)
  renewed=client('renew',ip=lease['ip'],mac=lease['mac']);assert renewed['ip']==lease['ip'];print('PASS renewal after active process failure',flush=True)
  if samba_enabled:
   wait(lambda:dns('cluster-renew.hosts.dhcp.test','A')==lease['ip'])
   wait(lambda:dns('150.2.0.192.in-addr.arpa','PTR')=='cluster-renew.hosts.dhcp.test.')
   wait(lambda:not dns('cluster-dora.hosts.dhcp.test','A'))
   assert dns('_godhcp-owner.cluster-renew.hosts.dhcp.test','TXT')
   print('PASS Samba A/PTR rename and cleanup after failover',flush=True)
  print(run('ip','netns','exec','client','python3','/lab/cluster6_wire.py','renew'),flush=True)
  # Restore the fenced node with the original data directory and remove its partition.
  processes[leader].wait(timeout=10)
  log=open(RESULTS/(leader+'.log'),'a')
  p=sp.Popen(processes[leader].args,stdout=log,stderr=sp.STDOUT);log.close();processes[leader]=p;Path('/tmp/'+leader+'.pid').write_text(str(p.pid))
  wait(lambda:'replica synchronized' in (RESULTS/(successor+'.log')).read_text())
  assert p.poll() is None and not owns(leader),'returning node must stay passive'
  previous=successor
  processes[previous].kill();processes[previous].wait()
  successor=wait(lambda:(n:=active()) and n!=previous and n)
  renewed=client('renew',ip=lease['ip'],mac=lease['mac']);assert renewed['ip']==lease['ip']
  print(run('ip','netns','exec','client','python3','/lab/cluster6_wire.py','renew'),flush=True)
  if samba_enabled:
   wait(lambda:dns('cluster-renew.hosts.dhcp.test','A')==lease['ip'])
   wait(lambda:dns('150.2.0.192.in-addr.arpa','PTR')=='cluster-renew.hosts.dhcp.test.')
  print('PASS returned replica and second daemon failover',previous,successor,flush=True)
  # Rejoin the stopped peer first: an already fenced solo owner may continue.
  log=open(RESULTS/(previous+'.log'),'a')
  p=sp.Popen(processes[previous].args,stdout=log,stderr=sp.STDOUT);log.close();processes[previous]=p
  wait(lambda:'replica synchronized' in (RESULTS/(successor+'.log')).read_text())
  # Without confirmed fencing a disconnected active must stop.
  Path('/tmp/deny-fence').touch()
  for n,p in processes.items():
   if n!=successor and p.poll() is None:p.kill();p.wait()
  wait(lambda:not any(owns(n) for n in 'ab'))
  failure=sp.run(['ip','netns','exec','client','python3','/lab/dhcp_lab.py','client',json.dumps(dict(iface='eth0',mode='renew',name='cluster-no-quorum',ip=lease['ip'],mac=lease['mac'],server=VIP))],capture_output=True,text=True)
  assert failure.returncode!=0 and 'no DHCP reply' in failure.stderr,failure.stderr
  print('PASS fencing unavailable withdraws VIP and stops DHCP',flush=True)
  print(run('ip','netns','exec','client','python3','/lab/cluster6_wire.py','silent'),flush=True)
  (RESULTS/'report.json').write_text(json.dumps(dict(status='PASS',mode='active-backup',samba_ddns=samba_enabled,leader=leader,successor=successor,lease=lease,checks=['daemon election','DORA','SIGKILL active process','fencing and VIP move','renew same IPv4','renew same IPv6 and DUID direct and relay','returned replica and second SIGKILL failover','Samba A/PTR failover' if samba_enabled else 'Samba disabled','fencing unavailable IPv4 and IPv6 silence']),indent=2))
 finally:
  for p in processes.values():
   if p.poll() is None:p.terminate()
  for p in processes.values():
   try:p.wait(timeout=20)
   except sp.TimeoutExpired:p.kill();p.wait()
  if samba is not None:
   os.killpg(samba.pid,signal.SIGTERM)
   try:samba.wait(timeout=15)
   except sp.TimeoutExpired:os.killpg(samba.pid,signal.SIGKILL);samba.wait()
if __name__=='__main__':
 if len(sys.argv)>1 and sys.argv[1]=='hook':hook(sys.argv[2],sys.argv[3])
 else:main()
