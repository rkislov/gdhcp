#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'python3 -c "import shutil,sys; shutil.rmtree(sys.argv[1])" "$work"' EXIT
GOBIN="$work" go install github.com/google/go-licenses/v2@v2.0.1
mkdir -p third_party
for arch in amd64 arm64; do
  GOOS=linux GOARCH="$arch" CGO_ENABLED=0 "$work/go-licenses" report ./cmd/godhcp > "third_party/go-licenses-$arch.csv"
done
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$work/go-licenses" save ./cmd/godhcp --save_path="$work/licenses" --ignore=github.com/kislovrs/godhcp
python3 - "$work/licenses" <<'PY'
import json,pathlib,shutil,subprocess,sys
root=pathlib.Path('third_party/licenses/go')
def copy_notice(source,destination):
 destination=pathlib.Path(destination)
 if destination.exists():destination.chmod(0o644)
 shutil.copyfile(source,destination);destination.chmod(0o644)
 return str(destination)
shutil.copytree(sys.argv[1],root,dirs_exist_ok=True,copy_function=copy_notice)
# The classifier doesn't collect every supplemental license or patent notice.
raw=subprocess.check_output(['go','list','-m','-json','all'],text=True)
decoder=json.JSONDecoder()
while raw.strip():
 module,offset=decoder.raw_decode(raw.lstrip());raw=raw.lstrip()[offset:]
 if module.get('Main') or not (root/module['Path']).exists():continue
 directory=pathlib.Path(module['Dir']);destination=root/module['Path']
 for pattern in ('LICENSE*','COPYING*','NOTICE*','AUTHORS*','PATENTS*'):
  for source in directory.glob(pattern):
   if source.is_file():destination.mkdir(parents=True,exist_ok=True);copy_notice(source,destination/source.name)
gostd=pathlib.Path('third_party/licenses/go-standard');gostd.mkdir(parents=True,exist_ok=True)
goroot=pathlib.Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
for name in ('LICENSE','PATENTS'):
 if (goroot/name).exists():shutil.copyfile(goroot/name,gostd/name)
web=pathlib.Path('third_party/licenses/tailwindcss');web.mkdir(parents=True,exist_ok=True)
shutil.copyfile('node_modules/tailwindcss/LICENSE',web/'LICENSE')
PY
