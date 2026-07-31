#!/bin/sh
set -e

CHROME_USER=pwuser
CHROME_HOME=$(getent passwd "$CHROME_USER" | cut -d: -f6)

echo "[goper-chrome] waiting for goper CA cert on /certs/ca-cert.pem..."
while [ ! -f /certs/ca-cert.pem ]; do
  sleep 1
done

echo "[goper-chrome] installing goper CA into system trust store..."
cp /certs/ca-cert.pem /usr/local/share/ca-certificates/goper.crt
update-ca-certificates

echo "[goper-chrome] adding goper CA to NSS database for $CHROME_USER..."
certutil -d sql:$CHROME_HOME/.pki/nssdb -N --empty-password >/dev/null 2>&1 || true
certutil -d sql:$CHROME_HOME/.pki/nssdb -A -t "C,," -n "goper" -i /certs/ca-cert.pem >/dev/null 2>&1 \
  || echo "[goper-chrome] WARN: certutil failed to add CA (continuing)"
chown -R "$CHROME_USER":"$CHROME_USER" "$CHROME_HOME/.pki"

CHROME_BIN=$(node -e "console.log(require('playwright').chromium.executablePath())")
echo "[goper-chrome] launching: $CHROME_BIN"

# Transparent mode: no --proxy-server flag. goper's iptables rules redirect
# this process's traffic to the proxy. Chrome runs as a non-root user so the
# uid-0 owner-skip rule (goper's own traffic) does not exempt it, and its
# sandbox stays enabled (no --no-sandbox).
exec setpriv --reuid="$CHROME_USER" --regid="$CHROME_USER" --init-groups env HOME="$CHROME_HOME" "$CHROME_BIN" \
  --disable-gpu \
  --disable-dev-shm-usage \
  --disable-quic \
  --remote-debugging-port=9222 \
  --remote-debugging-address=0.0.0.0 \
  --remote-allow-origins='*' \
  --user-data-dir="$CHROME_HOME/.config/goper-chrome" \
  --password-store=basic \
  https://example.com
