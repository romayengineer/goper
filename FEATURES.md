# goper — Implemented Features

Tracking document for implemented features (checked) and new feature ideas (open).

---

## Implemented

### Proxy & MITM (`internal/proxy`)

- [x] HTTP forward proxying via `github.com/elazarl/goproxy`
- [x] HTTPS interception via CONNECT + always-MITM
- [x] Root CA generation (ECDSA P-256), persisted to disk and reloaded on restart
- [x] Per-host leaf certificate generation, cached in memory (mutex-guarded)
- [x] Leaf certs include DNS SANs (or IP SANs when host is an IP)
- [x] 10-year CA expiry, 1-year leaf cert expiry
- [x] Transparent mode (Linux): original destination via `SO_ORIGINAL_DST`
- [x] TLS ClientHello SNI extraction via non-destructive `bufio.Reader.Peek`
- [x] First-byte sniffing to branch between plain HTTP and TLS (`0x16`)
- [x] Fallback to original destination IP when no SNI is present
- [x] In-memory per-connection proxy loop (`singleConnListener`) for transparent mode
- [x] Rewrites relative transparent-style request URLs to absolute before proxying
- [x] Transparent interception wired into the docker-compose workflow (`network_mode: service:goper` + iptables REDIRECT, zero app config)
- [x] TLS 1.2+ minimum, `http/1.1` ALPN only

### Capture (`internal/capture`)

- [x] Captures method, URL, scheme, host, path, request headers + body
- [x] Captures status code, response headers + body, content type, duration
- [x] Sensitive header redaction (`authorization`, `cookie`, `set-cookie`, `proxy-authorization`)
- [x] Response body cap of 1MB (larger bodies skipped)
- [x] Configurable body size limits: `--request-body-limit` / `--response-body-limit` (bytes; 0 = unlimited; response defaults to 1 MiB)
- [x] Binary/non-printable body detection (skipped unless JSON content type)
- [x] Request body restored after read so proxying still works
- [x] URL filtering: `--capture-include` / `--capture-exclude` regexes against the full URL; filtered traffic is proxied but neither stored nor written to outputs
- [x] Entry IDs: `YYYYMMDDHHMMSS-<counter>` format, unique, auto-assigned on push
- [x] Thread-safe in-memory ring buffer with configurable capacity (default 10,000)
- [x] Auto-eviction of oldest entries at capacity (eviction count tracked in stats)
- [x] O(1) lookup by ID via index map
- [x] Filtering: since, method, status, URL + limit/offset pagination
- [x] Clear, Len, subscriber channels for live feeds
- [x] Store stats: lifetime bytes captured + eviction/uptime counters

### HTTP API (`internal/api`)

- [x] `GET /api/requests` — paginated list with filters
- [x] `GET /api/requests/{id}` — single entry detail
- [x] `GET /api/requests/stream` — SSE live feed with flush support
- [x] SSE backfill: `?backfill=N` replays the N most recent entries on connect (default 50, `0` disables)
- [x] `DELETE /api/requests` — clear captured data
- [x] `GET /api/ca.pem` — download root CA cert
- [x] `GET /api/stats` — count, capacity, evictions, bytes captured, uptime, start time
- [x] `POST /api/requests/{id}/replay` — re-sends a captured request (method, headers, body) to its original URL and returns the fresh response; hop-by-hop headers (Host, Content-Length, …) stripped
- [x] Web UI dashboard at `/` (also `/ui`, `/index.html`) — embedded single-file HTML page, no build step or CDN: live table over SSE, method/status/URL filters, entry detail with pretty-printed bodies, stats bar, clear + CA download
- [x] CORS middleware (`*` origin, GET/POST/PUT/DELETE/OPTIONS)
- [x] Panic recovery middleware
- [x] Request logging middleware (method, path, status, bytes, duration)

### Output (`internal/output`)

- [x] `--output-dir` writes each JSON response body as pretty `.json` (`<id>.json`) organized per domain: `captures/<domain>/<id>.json`
- [x] `--output-format ndjson` appends compact JSON bodies to `captures/<domain>/responses.jsonl` (one stream per domain)
- [x] Domain folder names sanitized (port stripped, unsafe chars replaced, path-traversal hosts like `..` → `unknown`)
- [x] Non-JSON content types and invalid JSON skipped
- [x] Filesystem abstraction for unit testing
- [x] Concurrent-safe append writer for NDJSON

### iptables (`internal/iptables`)

- [x] `Setup`: installs NAT rules (owner-UID skip + port 80/443 REDIRECT to proxy)
- [x] Idempotent install (skips already-existing rules via `-C`)
- [x] `Teardown`: removes rules on shutdown (per-rule errors non-fatal)
- [x] Command runner abstraction for testing; `CAP_NET_ADMIN`/root required

### CLI & Runtime (`cmd/goper`, `internal/config`)

- [x] Flags: `--port`, `--api-port`, `--ca-dir`, `--transparent`, `--verbose`, `--buffer`, `--output-dir`, `--output-format`, `--log-format`, `--request-body-limit`, `--response-body-limit`, `--capture-include`, `--capture-exclude`
- [x] Flag validation fails fast on invalid regexes / output formats
- [x] `~` expansion in `--ca-dir`
- [x] Text or JSON structured logging via `log/slog`
- [x] SIGINT/SIGTERM graceful shutdown with iptables teardown
- [x] Pre-flight check: transparent mode fails fast with an actionable error if not running as root/CAP_NET_ADMIN
- [x] Runs proxy and API servers concurrently
- [x] Config defaults: proxy :8080, API :8081, buffer 10,000, CA at `~/.goper/ca`

### Docker & Deployment

- [x] Multi-stage `Dockerfile` (alpine runtime, static binary, runs as root for iptables management)
- [x] `docker-compose.yml` — goper (`cap_add: NET_ADMIN`, `--transparent`) + windowed Chrome sharing goper's network namespace (`network_mode: service:goper`), shared CA volume, captures bind-mount
- [x] Cross-platform DISPLAY: browser entrypoint resolves a usable X display at container start (keeps a working `DISPLAY`; falls back to a local unix socket, then XQuartz TCP via `host.docker.internal:0` on macOS) — works with plain `docker compose up`, not just `make up`
- [x] `make setup-mac` executes the XQuartz config (nolisten_tcp=false, restart, `xhost +`) instead of just printing instructions
- [x] Transparent interception enabled in the compose stack: iptables REDIRECT of ports 80/443, zero proxy config in the browser
- [x] Chrome runs as a non-root user (`pwuser`) so the proxy's uid-0 owner-skip rule does not exempt its traffic
- [x] Chrome runs with its sandbox enabled (no `--no-sandbox`); `seccomp:unconfined` on the chrome service lets it create user namespaces under Docker's default seccomp
- [x] Chromium's "Google API keys are missing" banner suppressed via a placeholder `GOOGLE_API_KEY` (Google-API-bound features intentionally disabled; harmless for capture)
- [x] On-device AI model downloads blocked: model directories (`OnDeviceHeadSuggestModel`, `OptGuideOnDeviceModel`, `optimization_guide_model_store`, `OnDeviceModelExecutables`) are root-writable only, so the browser user cannot persist models
- [x] Translate prompt disabled: managed policy `TranslateEnabled: false` + `translate.enabled: false` profile pref (the `--disable-translate` switch alone is ineffective in this Chromium build)
- [x] Translate e2e test: probes `chrome://translate-internals` counts after visiting a Spanish page (`es.wikipedia.org`) to assert no translate offer fires
- [x] Geolocation blocked by default: managed policy `DefaultGeolocationSetting: 2` + `default_content_setting_values.geolocation: 2` profile pref (no page can open the location popup)
- [x] Geolocation e2e test: probes permission state + `getCurrentPosition` pending-check on `where-am-i.co` to assert the location popup never appears
- [x] `docker-compose.integration.yml` — Xvfb overlay for headless/CI runs
- [x] Healthchecks for goper (API stats) and chrome (CDP version)
- [x] `browser/` image: installs goper CA into system + NSS trust stores, launches Chrome with no proxy configuration
- [x] `browser/playwright-example.js` — drives the running Chrome via CDP through goper
- [x] `Makefile` targets: build, run, docker, up, down, test (unit/integration/e2e/mac), cover, lint, clean

### Tests

- [x] Unit tests: config, capture, ring buffer (incl. concurrency + eviction), API handlers/routes/SSE, proxy handlers, cert cache, SNI parser (synthetic ClientHello vectors), iptables (mock runner), file writers (fake FS)
- [x] CA-reload regression test: a CA loaded from disk carries its private key and can sign leaf certs (previously the reload path left the key nil, breaking HTTPS MITM with `ERR_SSL_PROTOCOL_ERROR`)
- [x] Integration tests (`-tags=integration`): HTTP proxy, HTTPS MITM, transparent HTTP/HTTPS over loopback, real-disk output writers, API over listener
- [x] E2E tests (`-tags=e2e`): full docker-compose workflow with Chrome + Playwright + capture assertion
- [x] E2E asserts real iptables rule installation in the goper container (owner-uid skip + REDIRECT 80/443)
- [x] E2E asserts Chromium runs with no `--proxy-server` (interception fully transparent)
- [x] macOS window e2e test (`-tags=e2e && darwin`) with XQuartz window assertion
- [x] Linux window e2e test (`-tags=e2e && linux`) with real X display window assertion

---

## New / Proposed Features

Use this section to track planned or requested features.

- [ ] Response modification (inject headers, rewrite bodies)
- [ ] Persistent storage (SQLite or file-based) for the ring buffer
- [ ] WebSocket frame capture (currently only upgrade headers)
- [ ] gRPC support (HTTP/2 proto-aware capture)
- [ ] QUIC / HTTP/3 interception (UDP; not currently redirected)
- [ ] Distributed capture → central aggregator
