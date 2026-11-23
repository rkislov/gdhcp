#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
# Full, isolated acceptance suite. No access to production hypervisors is used.
set -euo pipefail
cd "$(dirname "$0")/.."
results=${GODHCP_PAIR_QUALIFICATION_RESULTS:-$PWD/output/active-backup-qualification}
mkdir -p "$results"
# Never leave an old PASS receipt after a failed new run.
rm -f "$results/acceptance.json"
go test -race ./... > "$results/go-race.log" 2>&1
go vet ./... > "$results/go-vet.log" 2>&1
GODHCP_PAIR_QUALIFY=1 go test -race ./internal/cluster -run 'TestPeerMutualTLS/pair_qualification' -v -count=1 > "$results/capacity.log" 2>&1
go test ./internal/cluster -run '^$' -fuzz FuzzPairImage -fuzztime 30s -parallel 2 > "$results/fuzz.log" 2>&1
python3 -m unittest discover -s tests/fencing -v > "$results/fencing-python.log" 2>&1
bash scripts/test-fencing-windows.sh > "$results/fencing-windows.log" 2>&1
GODHCP_PAIR_RESULTS="$results/vm" GODHCP_PAIR_SOAK_SECONDS="${GODHCP_PAIR_SOAK_SECONDS:-180}" bash scripts/test-active-backup-vm.sh > "$results/vm.log" 2>&1
GODHCP_CLUSTER_MODE=active-backup GODHCP_CLUSTER_RESULTS="$results/ddns" bash scripts/test-cluster-docker.sh > "$results/ddns.log" 2>&1
python3 - "$results" <<'PY'
import datetime,hashlib,json,pathlib,subprocess,sys
root=pathlib.Path(sys.argv[1]);vm=json.loads((root/'vm/report.json').read_text());dns=json.loads((root/'ddns/report.json').read_text())
assert 'error' not in vm and len(vm['results'])>=14 and all(x['status']=='PASS' for x in vm['results'])
soak=next(x for x in vm['results'] if x['name']=='continuous IPv4 and IPv6 renewals with stable role')
assert soak['seconds']>=180 and soak['rounds']>=20
assert len(vm['fencing'])>=3 and all(x['power_off_confirmed'] for x in vm['fencing'])
assert dns['status']=='PASS' and dns['mode']=='active-backup' and dns['samba_ddns']
report={'status':'PASS','completed_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source_commit':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'working_tree_dirty':bool(subprocess.check_output(['git','status','--porcelain'],text=True).strip()),'vm':vm,'ddns':dns,'logs':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in root.glob('*.log')}}
(root/'acceptance.json').write_text(json.dumps(report,indent=2)+'\n')
print('PASS active-backup acceptance:',root/'acceptance.json')
PY
