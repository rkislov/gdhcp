#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/pki"
openssl req -new -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=ClusterTest \
 -keyout "$work/ca.key" -out "$work/pki/ca.pem" >/dev/null 2>&1
for n in a b c; do
 openssl req -new -newkey rsa:2048 -nodes -subj "/CN=$n" -keyout "$work/pki/$n.key" -out "$work/$n.csr" >/dev/null 2>&1
 cat > "$work/ext" <<EXT
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth,clientAuth
subjectAltName=DNS:$n.test,URI:spiffe://godhcp/cluster/lab/node/$n
EXT
 openssl x509 -req -in "$work/$n.csr" -CA "$work/pki/ca.pem" -CAkey "$work/ca.key" -CAcreateserial -days 1 -extfile "$work/ext" -out "$work/pki/$n.pem" >/dev/null 2>&1
done
case "$(docker info --format '{{.Architecture}}')" in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) exit 1;; esac
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$work/godhcp" ./cmd/godhcp
mkdir "$work/tests"
cp -R tests/integration "$work/tests/"
cp scripts/godhcp-cluster-ip "$work/tests/integration/cluster_ip.py"
samba=${GODHCP_CLUSTER_SAMBA:-1}
if [ "$samba" = 1 ]; then
 docker build -t godhcp-ddns-samba-test:local tests/integration/ddns
 docker build -t godhcp-cluster-lab:local -f tests/integration/cluster/Dockerfile "$work"
else
 docker build -t godhcp-cluster-lab:local -f tests/integration/Dockerfile "$work"
fi
results=${GODHCP_CLUSTER_RESULTS:-$(pwd)/output/cluster-lab}
mkdir -p "$results"
docker run --rm --privileged --network none -e GODHCP_CLUSTER_SAMBA="$samba" -v "$work/pki:/pki:ro" -v "$results:/results" \
 --entrypoint python3 godhcp-cluster-lab:local -u /lab/cluster_lab.py
