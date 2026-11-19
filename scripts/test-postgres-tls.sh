#!/usr/bin/env bash
# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
name="godhcp-tls-$$"
pg_image=${GODHCP_TEST_POSTGRES_IMAGE:-postgres:16-alpine}
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker network rm "$name" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
mkdir "$work/tls"
# Disposable test CA only. Production CA keys must remain off the servers.
openssl req -new -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=TestCA \
  -keyout "$work/ca.key" -out "$work/tls/ca.pem" >/dev/null 2>&1
openssl req -new -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=OtherCA \
  -keyout "$work/other.key" -out "$work/tls/other-ca.pem" >/dev/null 2>&1
for item in db node rogue; do
  case "$item" in
    db) cn=db.example.net; usage=serverAuth ;;
    node) cn=dhcp-a; usage=clientAuth ;;
    rogue) cn=outsider; usage=clientAuth ;;
  esac
  openssl req -new -newkey rsa:2048 -nodes -subj "/CN=$cn" \
    -keyout "$work/tls/$item.key" -out "$work/$item.csr" >/dev/null 2>&1
  cat > "$work/ext" <<EXT
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=$usage
subjectAltName=DNS:$cn
EXT
  openssl x509 -req -in "$work/$item.csr" -CA "$work/tls/ca.pem" \
    -CAkey "$work/ca.key" -CAcreateserial -days 1 -sha256 -extfile "$work/ext" \
    -out "$work/tls/$item.pem" >/dev/null 2>&1
done
cat > "$work/tls/hba" <<'HBA'
local all all trust
hostssl godhcp godhcp 0.0.0.0/0 cert map=godhcp_nodes
host all all 0.0.0.0/0 reject
host all all ::/0 reject
HBA
cat > "$work/tls/ident" <<'IDENT'
godhcp_nodes dhcp-a godhcp
IDENT
case "$(docker info --format '{{.Architecture}}')" in
 aarch64|arm64) arch=arm64 ;;
 x86_64|amd64) arch=amd64 ;;
 *) echo 'Unsupported Docker architecture' >&2; exit 1 ;;
esac
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$work/tls/probe" tests/integration/postgres_tls_probe.go
# Server copies its key to a private postgres-owned file before starting.
chmod 755 "$work/tls"
chmod 644 "$work/tls/"*.pem "$work/tls/db.key" "$work/tls/hba" "$work/tls/ident"
chmod 600 "$work/tls/node.key" "$work/tls/rogue.key"
docker network create --internal "$name" >/dev/null
docker run -d --name "$name" --network "$name" --network-alias db.example.net --network-alias wrong.example.net \
  -e POSTGRES_PASSWORD=disposable-test-only -e POSTGRES_USER=postgres -e POSTGRES_DB=godhcp \
  -v "$work/tls:/tls:ro" --entrypoint sh "$pg_image" -ec '
    cp /tls/db.key /tmp/server.key
    chown postgres:postgres /tmp/server.key
    chmod 600 /tmp/server.key
    exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tls/db.pem \
      -c ssl_key_file=/tmp/server.key -c ssl_ca_file=/tls/ca.pem \
      -c hba_file=/tls/hba -c ident_file=/tls/ident
  ' >/dev/null
ready=false
for attempt in $(seq 1 60); do
 if docker exec "$name" pg_isready -U postgres -d godhcp >/dev/null 2>&1; then ready=true; break; fi
 sleep 1
done
if [ "$ready" != true ]; then docker logs "$name"; exit 1; fi
# Initdb briefly runs a socket-only server. Wait for the final TCP listener too.
for attempt in $(seq 1 30); do
 if docker exec "$name" pg_isready -h 127.0.0.1 -U postgres -d godhcp >/dev/null 2>&1; then break; fi
 sleep 1
done
docker exec "$name" psql -U postgres -d godhcp -v ON_ERROR_STOP=1 \
  -c 'CREATE ROLE godhcp LOGIN; ALTER DATABASE godhcp OWNER TO godhcp;' >/dev/null
docker run --rm --network "$name" -v "$work/tls:/tls:ro" --entrypoint /tls/probe "$pg_image"
