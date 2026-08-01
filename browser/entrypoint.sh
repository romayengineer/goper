#!/usr/bin/env bash
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

# Block Chrome's on-device AI model downloads: make the model directories
# root-writable only so the browser user (pwuser) cannot persist models.
UDD="$CHROME_HOME/.config/goper-chrome"
for d in OnDeviceHeadSuggestModel OptGuideOnDeviceModel optimization_guide_model_store OnDeviceModelExecutables; do
  mkdir -p "$UDD/$d"
  chown root:root "$UDD/$d"
  chmod 0755 "$UDD/$d"
done

# Disable Chrome's translate offer and geolocation via profile preferences (in
# addition to the managed policies). These mirror the chrome://settings toggles:
# translate.enabled=false, default_content_setting_values.geolocation=2 (Block).
DEFAULT_DIR="$UDD/Default"
mkdir -p "$DEFAULT_DIR"
chown "$CHROME_USER":"$CHROME_USER" "$DEFAULT_DIR"
if [ ! -f "$DEFAULT_DIR/Preferences" ]; then
  printf '{"translate":{"enabled":false},"profile":{"default_content_setting_values":{"geolocation":2}}}\n' > "$DEFAULT_DIR/Preferences"
  chown "$CHROME_USER":"$CHROME_USER" "$DEFAULT_DIR/Preferences"
fi

# --- Resolve a usable X display -------------------------------------------
# Docker Desktop for macOS runs containers in a Linux VM where the Mac host's
# X11 unix socket does not exist; the only way to reach XQuartz is TCP via the
# host gateway (host.docker.internal:0), which requires XQuartz to listen on
# :6000 (nolisten_tcp=false) and accept the connection (xhost +). On native
# Linux the mounted /tmp/.X11-unix socket works as-is. The CI/integration
# overlay (in-container Xvfb :99) is also covered.
resolve_display() {
  # 1. The provided DISPLAY already answers? Keep it (Linux desktop, Xvfb overlay).
  if [ -n "${DISPLAY:-}" ] && timeout 5 xdpyinfo >/dev/null 2>&1; then
    return 0
  fi
  # 2. A local unix socket is visible (native Linux with the socket mount)?
  for sock in /tmp/.X11-unix/X*; do
    [ -e "$sock" ] || continue
    export DISPLAY=":${sock##*X}"
    if timeout 5 xdpyinfo >/dev/null 2>&1; then
      echo "[goper-chrome] DISPLAY=$DISPLAY (local unix socket)"
      return 0
    fi
  done
  # 3. Docker Desktop / WSL2: X over TCP via the host gateway.
  export DISPLAY=host.docker.internal:0
  if timeout 5 xdpyinfo >/dev/null 2>&1; then
    echo "[goper-chrome] DISPLAY=$DISPLAY (TCP via host gateway)"
    return 0
  fi
  echo "[goper-chrome] FATAL: no usable X display. On macOS run: 'make setup-mac' (or: defaults write org.xquartz.X11 nolisten_tcp -bool false; open -a XQuartz; xhost +). On Linux: xhost +local:" >&2
  return 1
}
resolve_display || exit 1
# Persist the resolved display so tools exec'd into the container (e.g. the
# e2e window assertion) see the same display as Chrome. The container's env
# DISPLAY is the raw compose value, which may be a Mac-only launchd path that
# does not exist inside the Docker VM.
echo "$DISPLAY" > /tmp/goper-display
chmod 0644 /tmp/goper-display
echo "[goper-chrome] final DISPLAY=$DISPLAY"

# Transparent mode: no --proxy-server flag. goper's iptables rules redirect
# this process's traffic to the proxy. Chrome runs as a non-root user so the
# uid-0 owner-skip rule (goper's own traffic) does not exempt it, and its
# sandbox stays enabled (no --no-sandbox). The translate offer is disabled by
# the managed policy + translate.enabled pref (see above).
#
# Interaction latency: --disable-backgrounding-occluded-windows and
# --disable-renderer-backgrounding stop Chrome from throttling renderers it
# believes are "occluded"/"backgrounded" (a misdetection that happens over
# remote X11 transports and makes typing/clicking feel laggy).
# --process-per-site keeps the renderer-process count low, which matters a
# lot on CPU-constrained VMs (e.g. Colima's default 2 vCPUs): fewer processes
# means far less scheduler contention for software rendering.
exec setpriv --reuid="$CHROME_USER" --regid="$CHROME_USER" --init-groups env HOME="$CHROME_HOME" "$CHROME_BIN" \
  --disable-gpu \
  --disable-dev-shm-usage \
  --disable-quic \
  --disable-translate \
  --disable-backgrounding-occluded-windows \
  --disable-renderer-backgrounding \
  --process-per-site \
  --remote-debugging-port=9222 \
  --remote-debugging-address=0.0.0.0 \
  --remote-allow-origins='*' \
  --user-data-dir="$CHROME_HOME/.config/goper-chrome" \
  --password-store=basic \
  https://example.com
