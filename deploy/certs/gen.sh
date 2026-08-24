#!/bin/sh
# Generate a development CA plus server and client certificates.
# These are for `make dev` only — never use them anywhere real.
set -e
cd "$(dirname "$0")"

SUBJ_CA="/CN=lazymqtt-dev-ca"
SUBJ_SRV="/CN=localhost"
SUBJ_CLI="/CN=lazymqtt-client"

openssl req -x509 -newkey rsa:2048 -days 825 -nodes \
  -keyout ca-key.pem -out ca.pem -subj "$SUBJ_CA" 2>/dev/null

for name in server client; do
  case $name in
    server) subj=$SUBJ_SRV ;;
    client) subj=$SUBJ_CLI ;;
  esac
  openssl req -newkey rsa:2048 -nodes \
    -keyout "$name-key.pem" -out "$name.csr" -subj "$subj" 2>/dev/null
  openssl x509 -req -in "$name.csr" -days 825 \
    -CA ca.pem -CAkey ca-key.pem -CAcreateserial \
    -extfile /dev/stdin -out "$name.pem" 2>/dev/null <<EXT
subjectAltName = DNS:localhost, IP:127.0.0.1
EXT
  rm -f "$name.csr"
done

# mosquitto reads these through a read-only bind mount and drops privileges to
# the `mosquitto` user, which is not the user that ran this script. openssl
# writes keys 0600, so the broker cannot read them and silently starts without
# its TLS listeners. These are throwaway certificates for `make dev` — the
# .gitignore keeps them out of the repository — so world-readable is fine and
# is the difference between the TLS listeners working and not.
chmod 0644 ./*.pem

echo "wrote ca.pem, server.pem, server-key.pem, client.pem, client-key.pem"
