#!/usr/bin/env bash
#
# Generate a throwaway PKI for testing the IHE listener (CARD-5 / CARD-6).
#
#   scripts/gen-ihe-certs.sh [extra-hostname ...]
#
# Produces, under certs/ihe/ (gitignored):
#
#   ca.pem            the development CA — trust this one, nothing else
#   ca-key.pem        its private key; only this script needs it
#   server.pem        what the listener presents          → ihe.cert_file
#   server-key.pem    its key                             → ihe.key_file
#   clients-ca.pem    copy of ca.pem                      → ihe.client_ca_file
#   client.pem        the Display's certificate           → curl --cert
#   client-key.pem    its key                             → curl --key
#   client.p12        the same client identity, for import into a browser
#                     or the macOS keychain. Passphrase: ecghub
#
# DEVELOPMENT ONLY. This CA signs anything, its key sits next to the
# certificates it issues, and the passphrase is written above in clear. A
# production deployment gets its certificates from hospital IT's PKI; see the
# `ihe:` section of config.example.yaml.
#
# The server certificate covers localhost and 127.0.0.1. Pass extra hostnames or
# IPs as arguments when the Display reaches the listener by another name:
#
#   scripts/gen-ihe-certs.sh ecg-hub.chu.local 10.0.0.12
#
set -euo pipefail

cd "$(dirname "$0")/.."
OUT="certs/ihe"
DAYS=825           # what browsers still accept for a leaf certificate
P12_PASS="ecghub"

mkdir -p "$OUT"

# Refuse to silently replace a PKI someone is already testing against: a new CA
# invalidates every client certificate already imported into a browser.
if [ -f "$OUT/ca.pem" ] && [ "${FORCE:-}" != "1" ]; then
  echo "error: $OUT already holds a CA."
  echo "       Re-running issues a new one and every client certificate already"
  echo "       imported into a browser stops working."
  echo "       Delete $OUT yourself, or re-run with FORCE=1, to confirm."
  exit 1
fi

# SANs. A certificate without them is rejected outright by every modern client,
# so the list is built before anything is signed.
SAN="DNS:localhost,IP:127.0.0.1,IP:::1"
for host in "$@"; do
  if printf '%s' "$host" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'; then
    SAN="$SAN,IP:$host"
  else
    SAN="$SAN,DNS:$host"
  fi
done

ext_file="$(mktemp)"
trap 'rm -f "$ext_file"' EXIT

echo "==> development CA"
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -keyout "$OUT/ca-key.pem" -out "$OUT/ca.pem" \
  -subj "/CN=ECG Hub IHE dev CA/O=ECG Hub (development)" 2>/dev/null

echo "==> server certificate ($SAN)"
cat > "$ext_file" <<EOF
basicConstraints = CA:FALSE
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = $SAN
EOF
openssl req -newkey rsa:2048 -nodes \
  -keyout "$OUT/server-key.pem" -out "$OUT/server.csr" \
  -subj "/CN=localhost/O=ECG Hub IHE listener" 2>/dev/null
openssl x509 -req -in "$OUT/server.csr" -days "$DAYS" \
  -CA "$OUT/ca.pem" -CAkey "$OUT/ca-key.pem" -CAcreateserial \
  -extfile "$ext_file" -out "$OUT/server.pem" 2>/dev/null

echo "==> client certificate (the Display actor)"
cat > "$ext_file" <<EOF
basicConstraints = CA:FALSE
keyUsage = critical, digitalSignature
extendedKeyUsage = clientAuth
EOF
# The Common Name lands in the audit trail as the actor of every IHE request
# (audit_logs.user_id = "ihe:<CN>"), so it names the Display, not the machine.
openssl req -newkey rsa:2048 -nodes \
  -keyout "$OUT/client-key.pem" -out "$OUT/client.csr" \
  -subj "/CN=dpi-display/O=ECG Hub IHE test Display" 2>/dev/null
openssl x509 -req -in "$OUT/client.csr" -days "$DAYS" \
  -CA "$OUT/ca.pem" -CAkey "$OUT/ca-key.pem" -CAcreateserial \
  -extfile "$ext_file" -out "$OUT/client.pem" 2>/dev/null

# A browser cannot use a PEM pair directly; it imports a PKCS#12 bundle.
openssl pkcs12 -export -legacy \
  -inkey "$OUT/client-key.pem" -in "$OUT/client.pem" -certfile "$OUT/ca.pem" \
  -name "ECG Hub IHE test Display" \
  -passout "pass:$P12_PASS" -out "$OUT/client.p12" 2>/dev/null \
  || openssl pkcs12 -export \
       -inkey "$OUT/client-key.pem" -in "$OUT/client.pem" -certfile "$OUT/ca.pem" \
       -name "ECG Hub IHE test Display" \
       -passout "pass:$P12_PASS" -out "$OUT/client.p12"

cp "$OUT/ca.pem" "$OUT/clients-ca.pem"
rm -f "$OUT/server.csr" "$OUT/client.csr"
chmod 600 "$OUT"/*-key.pem "$OUT/client.p12"

cat <<EOF

Done — $OUT

config.yaml:

  ihe:
    enabled: true
    port: 8443
    cert_file: $PWD/$OUT/server.pem
    key_file: $PWD/$OUT/server-key.pem
    client_ca_file: $PWD/$OUT/clients-ca.pem

Check the handshake (expects the WSDL back):

  curl --cacert $OUT/ca.pem \\
       --cert $OUT/client.pem --key $OUT/client-key.pem \\
       https://localhost:8443/ihe/retrieve-for-display.wsdl

Without the client certificate the same call must fail — that is the test:

  curl --cacert $OUT/ca.pem https://localhost:8443/ihe/retrieve-for-display.wsdl

Browser (to drive tools/ihe-display.html):
  1. import $OUT/client.p12  — passphrase: $P12_PASS
  2. trust $OUT/ca.pem, otherwise the page cannot load over HTTPS
     macOS: open the file, then set it to "Always Trust" in Keychain Access
  3. the browser asks which certificate to present on first connection

EOF
