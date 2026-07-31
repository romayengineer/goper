#!/bin/sh
set -e

echo "[goper-chrome] waiting for goper CA cert on /certs/ca-cert.pem..."
while [ ! -f /certs/ca-cert.pem ]; do
  sleep 1
done

echo "[goper-chrome] installing goper CA into system trust store..."
cp /certs/ca-cert.pem /usr/local/share/ca-certificates/goper.crt
update-ca-certificates

echo "[goper-chrome] adding goper CA to NSS database..."
mkdir -p /root/.pki/nssdb
certutil -d sql:/root/.pki/nssdb -N --empty-password >/dev/null 2>&1 || true
certutil -d sql:/root/.pki/nssdb -A -t "C,," -n "goper" -i /certs/ca-cert.pem >/dev/null 2>&1 \
  || echo "[goper-chrome] WARN: certutil failed to add CA (continuing)"

CHROME_BIN=$(node -e "console.log(require('playwright').chromium.executablePath())")
echo "[goper-chrome] launching: $CHROME_BIN"

exec "$CHROME_BIN" \
  --no-sandbox \
  --disable-gpu \
  --disable-dev-shm-usage \
  --proxy-server=http://goper:8080 \
  --remote-debugging-port=9222 \
  --remote-debugging-address=0.0.0.0 \
  --remote-allow-origins='*' \
  --user-data-dir=/root/.config/goper-chrome \
  --password-store=basic \
  https://example.com
