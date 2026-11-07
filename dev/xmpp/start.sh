#!/bin/sh
set -eu

# Development-only certificate and fixture accounts. Persist these with the data,
# rather than resetting credentials or identities whenever the container restarts.
certs=/var/lib/prosody/certs
mkdir -p "$certs"
if [ ! -f "$certs/localhost.key" ]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
    -subj /CN=localhost -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' \
    -keyout "$certs/localhost.key" -out "$certs/localhost.crt"
fi
chmod 600 "$certs/localhost.key"
chown -R prosody:prosody /var/lib/prosody
chown prosody:prosody /var/run/prosody
if [ ! -f /var/lib/prosody/.oie-fixtures-v1 ]; then
  prosodyctl --config /etc/prosody/prosody.cfg.lua register alice localhost alice-local-only
  prosodyctl --config /etc/prosody/prosody.cfg.lua register bob localhost bob-local-only
  touch /var/lib/prosody/.oie-fixtures-v1
  chown prosody:prosody /var/lib/prosody/.oie-fixtures-v1
fi
exec /entrypoint.sh prosody -F --config /etc/prosody/prosody.cfg.lua
