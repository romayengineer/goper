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
- [x] TLS 1.2+ minimum, `http/1.1` ALPN only

### Capture (`internal/capture`)

- [x] Captures method, URL, scheme, host, path, request headers + body
- [x] Captures status code, response headers + body, content type, duration
- [x] Sensitive header redaction (`authorization`, `cookie`, `set-cookie`, `proxy-authorization`)
- [x] Response body cap of 1MB (larger bodies skipped)
- [x] Binary/non-printable body detection (skipped unless JSON content type)
- [x] Request body restored after read so proxying still works
- [x] Entry IDs: `YYYYMMDDHHMMSS-<counter>` format, unique, auto-assigned on push
- [x] Thread-safe in-memory ring buffer with configurable capacity (default 10,000)
- [x] Auto-eviction of oldest entries at capacity
- [x] O(1) lookup by ID via index map
- [x] Filtering: since, method, status, URL + limit/offset pagination
- [x] Clear, Len, subscriber channels for live feeds

### HTTP API (`internal/api`)

- [x] `GET /api/requests` — paginated list with filters
- [x] `GET /api/requests/{id}` — single entry detail
- [x] `GET /api/requests/stream` — SSE live feed (with flush support)
- [x] `DELETE /api/requests` — clear captured data
- [x] `GET /api/ca.pem` — download root CA cert
- [x] `GET /api/stats` — entry count
- [x] CORS middleware (`*` origin, GET/POST/PUT/DELETE/OPTIONS)
- [x] Panic recovery middleware
- [x] Request logging middleware (method, path, status, bytes, duration)

### Output (`internal/output`)

- [x] `--output-dir` writes each JSON response body as pretty `.json` (`<id>.json`)
- [x] `--output-format ndjson` appends compact JSON bodies to a single `.jsonl`
- [x] Non-JSON content types and invalid JSON skipped
- [x] Filesystem abstraction for unit testing
- [x] Concurrent-safe append writer for NDJSON

### iptables (`internal/iptables`)

- [x] `Setup`: installs NAT rules (owner-UID skip + port 80/443 REDIRECT to proxy)
- [x] Idempotent install (skips already-existing rules via `-C`)
- [x] `Teardown`: removes rules on shutdown (per-rule errors non-fatal)
- [x] Command runner abstraction for testing; `CAP_NET_ADMIN`/root required

### CLI & Runtime (`cmd/goper`, `internal/config`)

- [x] Flags: `--port`, `--api-port`, `--ca-dir`, `--transparent`, `--verbose`, `--buffer`, `--output-dir`, `--output-format`, `--log-format`
- [x] `~` expansion in `--ca-dir`
- [x] Text or JSON structured logging via `log/slog`
- [x] SIGINT/SIGTERM graceful shutdown with iptables teardown
- [x] Runs proxy and API servers concurrently
- [x] Config defaults: proxy :8080, API :8081, buffer 10,000, CA at `~/.goper/ca`

### Docker & Deployment

- [x] Multi-stage `Dockerfile` (alpine runtime, static binary, unprivileged `goper` user via `su-exec`)
- [x] `docker-compose.yml` — goper + windowed Chrome (CDP :9222, X11), shared CA volume, captures bind-mount
- [x] `docker-compose.integration.yml` — Xvfb overlay for headless/CI runs
- [x] Healthchecks for goper (API stats) and chrome (CDP version)
- [x] `browser/` image: installs goper CA into system + NSS trust stores, launches Chrome pointed at proxy
- [x] `browser/playwright-example.js` — drives the running Chrome via CDP through goper
- [x] `Makefile` targets: build, run, docker, up, down, test (unit/integration/e2e/mac), cover, lint, clean

### Tests

- [x] Unit tests: config, capture, ring buffer (incl. concurrency + eviction), API handlers/routes/SSE, proxy handlers, cert cache, SNI parser (synthetic ClientHello vectors), iptables (mock runner), file writers (fake FS)
- [x] Integration tests (`-tags=integration`): HTTP proxy, HTTPS MITM, transparent HTTP/HTTPS over loopback, real-disk output writers, API over listener
- [x] E2E tests (`-tags=e2e`): full docker-compose workflow with Chrome + Playwright + capture assertion
- [x] macOS window e2e test (`-tags=e2e && darwin`) with XQuartz window assertion

---

## New / Proposed Features

Use this section to track planned or requested features.

- [ ] Response modification (inject headers, rewrite bodies)
- [ ] Request replay (resend captured requests)
- [ ] Persistent storage (SQLite or file-based) for the ring buffer
- [ ] Web UI dashboard for browsing captured traffic
- [ ] WebSocket frame capture (currently only upgrade headers)
- [ ] gRPC support (HTTP/2 proto-aware capture)
- [ ] QUIC / HTTP/3 interception (UDP; not currently redirected)
- [ ] Distributed capture → central aggregator
- [ ] URL filtering rules (only capture URLs matching a pattern)
- [ ] Request body size limit flag (currently unlimited)
- [ ] Configurable response body size limit (currently hardcoded 1MB)
- [ ] Stats endpoint enrichment (bytes captured, uptime, evictions)
- [ ] SSE replay/backfill of historical entries on connect
