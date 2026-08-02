# Running goper natively on macOS (no Docker)

This guide runs goper and Google Chrome directly on your Mac — no Docker, no
containers — for the fastest possible iteration speed. You get the same proxy,
MITM capture, and dashboard, just without container overhead.

> **The one difference vs the Docker stack:** transparent interception is
> Linux-only (it relies on `SO_ORIGINAL_DST` + iptables REDIRECT). On macOS
> goper runs as a regular **forward proxy**, so Chrome must be configured to
> use it explicitly, and HTTPS trust is handled through the macOS Keychain.

---

## 1. Prerequisites

- macOS (Apple Silicon or Intel)
- [Go](https://go.dev/dl/) **≥ 1.26.5** (`go version` to check)
- [Google Chrome](https://www.google.com/chrome/) installed in `/Applications`
- No Docker required.

## 2. Build & run goper

From the repo root:

```sh
make build        # produces ./goper
```

Then start it with the ports and capture settings you want (all flags are
optional — defaults are proxy `:8080`, API `:8081`, CA at `~/.goper/ca`, and
JSON captures enabled to `./captures`):

```sh
./goper \
  --port 8080 \
  --api-port 8081 \
  --ca-dir ~/.goper/ca \
  --output-dir ./captures \
  --output-format json \
  --verbose
```

Notes:

- **JSON capture is on by default** — every response body that parses as JSON
  is written to `./captures/<domain>/<id>.json` (regardless of its
  `Content-Type`). `--output-dir` changes the location, and `--no-capture`
  turns disk capture off entirely (the live dashboard still shows requests).

- **Do not pass `--transparent`** — it requires Linux + root/CAP_NET_ADMIN and
  will fail fast on macOS.
- The first run auto-generates a CA at `~/.goper/ca/ca-cert.pem`
  (`--ca-dir` overrides the location). The certificate is served over HTTP at
  `http://127.0.0.1:8081/api/ca.pem` so you can download it later.
- The API dashboard lives at `http://127.0.0.1:8081`.
- Fastest one-liner if you just want defaults + debug logs:
  `make run` (equivalent to `go run ./cmd/goper --verbose`).
- To build without `make`: `go build -o goper ./cmd/goper`.

## 3. Trust goper's CA in the macOS Keychain (one-time)

Chrome validates HTTPS certificates against the macOS trust store. Because
goper MITMs HTTPS, Chrome must trust goper's root CA or every HTTPS site shows
`NET::ERR_CERT_AUTHORITY_INVALID`. Install it as a trusted root:

```sh
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain \
  ~/.goper/ca/ca-cert.pem
```

Prefer no `sudo`? Add it to your **login** keychain instead:

```sh
security add-trusted-cert -d -r trustRoot \
  -k ~/Library/Keychains/login.keychain-db \
  ~/.goper/ca/ca-cert.pem
```

To download the cert via the API instead of the file path:

```sh
curl -s http://127.0.0.1:8081/api/ca.pem -o /tmp/goper-ca.pem
```

> **Quick dev alternative** (not recommended for everyday use): skip the
> Keychain step and add `--ignore-certificate-errors` to the Chrome command
> below. This bypasses *all* certificate errors, which is convenient but
> broadly insecure.

## 4. Configure Google Chrome to use the proxy

Launch Chrome with a **dedicated profile** and point it at goper. The fresh
`--user-data-dir` keeps the proxy isolated from your normal browsing profile
and avoids the "existing Chrome instance ignores the flag" problem:

```sh
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --user-data-dir=/tmp/goper-chrome \
  --proxy-server="http://127.0.0.1:8080" \
  --proxy-bypass-list="<-loopback>" \
  --disable-quic
```

Flag breakdown:

| Flag | Why |
|------|-----|
| `--user-data-dir=/tmp/goper-chrome` | Fresh isolated profile; avoids interfering with your normal Chrome sessions |
| `--proxy-server="http://127.0.0.1:8080"` | Send HTTP and HTTPS (via CONNECT) through goper |
| `--proxy-bypass-list="<-loopback>"` | Keep `localhost`/`127.0.0.1` traffic (e.g. the dashboard) out of the proxy |
| `--disable-quic` | QUIC/HTTP-3 is UDP and would bypass the TCP proxy and capture |

You can make this a reusable command:

```sh
alias goper-chrome='"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --user-data-dir=/tmp/goper-chrome --proxy-server="http://127.0.0.1:8080" --proxy-bypass-list="<-loopback>" --disable-quic'
```

> **Gotcha:** if a regular Chrome is already running with your default profile,
> a second launch may just open a tab in that instance (ignoring the proxy
> flags). The dedicated `--user-data-dir` avoids this entirely — that profile
> can only be used by this invocation.

## 5. Verify it works

With goper running, from another terminal:

HTTP through the proxy:

```sh
curl -s -x http://127.0.0.1:8080 http://example.com/ -o /dev/null -w '%{http_code}\n'
# 200
```

HTTPS through the proxy (MITM) — confirm the leaf is issued by goper's CA:

```sh
openssl s_client -proxy 127.0.0.1:8080 -connect example.com:443 -showcerts </dev/null 2>/dev/null | grep -i "goper MITM CA"
# issuer=CN = goper MITM CA
```

Then:

1. Open the dashboard: `open http://127.0.0.1:8081` — browse to a few sites in
   the proxied Chrome and watch requests appear in the table (live over SSE).
2. By default, pretty-printed JSON bodies land in
   `./captures/<domain>/<id>.json` (disable with `--no-capture`, or move with
   `--output-dir`).

## 6. Stop & clean up

- Stop goper with `Ctrl-C` (graceful shutdown).
- Close the proxied Chrome and remove its temp profile:

  ```sh
  rm -rf /tmp/goper-chrome
  ```

- Revoke CA trust when you're done with it:

  ```sh
  sudo security delete-certificate -c "goper MITM CA" \
    -k /Library/Keychains/System.keychain
  ```

  (omit `sudo` and use your login keychain if you installed it there).

## 7. Troubleshooting

| Symptom | Fix |
|---------|-----|
| `NET::ERR_CERT_AUTHORITY_INVALID` on HTTPS sites | goper's CA isn't trusted — run the Keychain step in §3, then quit and relaunch Chrome |
| Chrome ignores `--proxy-server` | An existing Chrome (default profile) is running — use the dedicated `--user-data-dir` or quit Chrome first |
| HTTPS sites time out / no CONNECT | Confirm goper is listening: `curl -s http://127.0.0.1:8081/api/stats` |
| `transparent mode requires running as root` / platform error | You passed `--transparent` — remove it (macOS only supports explicit proxy mode) |
| `listen :8080: address already in use` | Another process holds the port — pass `--port` to a free port and update `--proxy-server` to match |
| Requests work but nothing is captured | Confirm you didn't pass `--no-capture`; check `--capture-include`/`--capture-exclude` filters and the API dashboard's filters; confirm the browser is using the proxied profile |

---

### Related

- `FEATURES.md` — full feature list (capture, API, output writers, filters).
- `docker-compose.yml` — the containerized stack (transparent mode on Linux).
