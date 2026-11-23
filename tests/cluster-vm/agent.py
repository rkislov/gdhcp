#!/usr/bin/env python3
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
"""Disposable VM fixture only; control port is confined to QEMU user networking."""
import http.server,json,subprocess,sys,urllib.request
from pathlib import Path
if len(sys.argv)>1 and sys.argv[1]=='fence':
    target=sys.argv[2]
    assert target in ('a','b','c')
    req=urllib.request.Request('http://10.0.2.2:19090/fence/'+target,data=b'',method='POST')
    with urllib.request.urlopen(req,timeout=15) as response:
        assert response.status==200
    sys.exit(0)
node=Path('/etc/pair-node').read_text().strip()
# virtio-mmio enumeration order is not a stable network identity.
links=json.loads(subprocess.check_output(['ip','-j','link'],text=True))
for link in links:
    mac=link.get('address','')
    if mac.startswith('52:54:00:00:01:') or mac.startswith('52:54:00:00:02:'):
        target='pair-service' if mac.startswith('52:54:00:00:01:') else 'pair-management'
        subprocess.run(['ip','link','set',link['ifname'],'down'],check=True)
        subprocess.run(['ip','link','set',link['ifname'],'name',target],check=True)
subprocess.run(['ip','link','set','pair-service','name','eth0'],check=True)
subprocess.run(['ip','link','set','pair-management','name','eth1'],check=True)

for args in (['link','set','eth0','up'],['link','set','eth1','up'],['addr','add','192.0.2.'+str(11+'abc'.index(node))+'/24','dev','eth0'],['addr','add','10.0.2.15/24','dev','eth1']):
    subprocess.run(['ip',*args],check=True)
subprocess.run(['ip','-6','addr','add','fd42:10::'+str(11+'abc'.index(node))+'/64','dev','eth0','nodad'],check=True)
class Agent(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        args=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        try:
            r=subprocess.run(args,capture_output=True,text=True,timeout=45)
            body=json.dumps(dict(code=r.returncode,out=r.stdout,err=r.stderr)).encode()
        except Exception as e:body=json.dumps(dict(code=-1,out='',err=str(e))).encode()
        self.send_response(200);self.end_headers();self.wfile.write(body)
    def log_message(self,*args):pass
server=http.server.ThreadingHTTPServer(('10.0.2.15',9000),Agent)
subprocess.run(['systemd-notify','--ready'],check=True)
server.serve_forever()
