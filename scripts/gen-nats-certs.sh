#!/usr/bin/env bash
# Generate a self-signed CA + NATS server certificate for the production
# overlay (docker-compose.prod.yml). Output is everything the prod stack's
# HARPORIS_NATS_CERT_DIR needs:
#
#     ca.pem            # the CA — clients trust this (NATS_ROOT_CAS)
#     server-cert.pem   # NATS server certificate
#     server-key.pem    # NATS server private key
#
# Usage:
#     scripts/gen-nats-certs.sh [OUTPUT_DIR] [SERVER_CN]
#
#     OUTPUT_DIR   where to write the files   (default: ./deploy/nats/certs)
#     SERVER_CN    DNS name clients connect to (default: nats)
#
# Example (matches docker-compose.prod.yml service name "nats"):
#     scripts/gen-nats-certs.sh /etc/ssl/harporis-nats nats
#
# Then point the prod overlay at it:
#     export HARPORIS_NATS_CERT_DIR=/etc/ssl/harporis-nats
#
# This covers the TLS half. For NATS JWT/nkey client auth (operator.jwt +
# client.creds) see docs/DEPLOYMENT.md — that uses the `nsc` tool.
set -euo pipefail

OUT_DIR="${1:-./deploy/nats/certs}"
SERVER_CN="${2:-nats}"
DAYS=825   # < 825 keeps it within common TLS validity limits

command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 1; }

mkdir -p "$OUT_DIR"
cd "$OUT_DIR"

echo "==> generating CA"
openssl genrsa -out ca-key.pem 4096 2>/dev/null
openssl req -x509 -new -nodes -key ca-key.pem -sha256 -days "$DAYS" \
    -subj "/CN=Harporis NATS CA" -out ca.pem

echo "==> generating server key + CSR (CN=$SERVER_CN)"
openssl genrsa -out server-key.pem 4096 2>/dev/null
openssl req -new -key server-key.pem -subj "/CN=$SERVER_CN" -out server.csr

cat > server-ext.cnf <<EOF
subjectAltName = DNS:$SERVER_CN, DNS:localhost, IP:127.0.0.1
extendedKeyUsage = serverAuth
EOF

echo "==> signing server certificate"
openssl x509 -req -in server.csr -CA ca.pem -CAkey ca-key.pem -CAcreateserial \
    -days "$DAYS" -sha256 -extfile server-ext.cnf -out server-cert.pem 2>/dev/null

rm -f server.csr server-ext.cnf ca.srl

echo
echo "Done. Files in $OUT_DIR:"
echo "  ca.pem          -> clients (NATS_ROOT_CAS / client ca.pem)"
echo "  server-cert.pem -> NATS server"
echo "  server-key.pem  -> NATS server (keep secret, mode 600)"
chmod 600 server-key.pem ca-key.pem
echo
echo "Next: export HARPORIS_NATS_CERT_DIR=$OUT_DIR  (see docs/DEPLOYMENT.md)"
