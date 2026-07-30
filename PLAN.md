# goper — Transparent MITM Proxy Sidecar

Intercept all HTTP/HTTPS traffic from a Docker container without any application configuration. Built in Go.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ Docker Host                                                     │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ Shared Network Namespace (service:goper)                 │   │
│  │                                                          │   │
│  │  [browser-app]                                           │   │
│  │    ↓  connects to https://api.example.com                │   │
│  │  [iptables PREROUTING + OUTPUT]                          │   │
│  │    ├─ -m owner --uid-owner goper -j RETURN               │   │
│  │    ├─ -p tcp --dport 80  -j REDIRECT --to-port 8080      │   │
│  │    └─ -p tcp --dport 443 -j REDIRECT --to-port 8080      │   │
│  │    ↓                                                     │   │
│  │  [goper:8080]  (user: goper)                             │   │
│  │    ├─ HTTP  → goproxy (reads Host header)                │   │
│  │    ├─ HTTPS → MITM via SNI + SO_ORIGINAL_DST             │   │
│  │    ├─ Capture request/response (JSON detection)          │   │
│  │    └─ Store in ring buffer                               │   │
│  │    ↓                                                     │   │
│  │  [goper as user "goper" → iptables SKIPS this traffic]   │   │
│  │  [upstream network → internet]                           │   │
│  │                                                          │   │
│  │  [goper:8081 HTTP API]  ←──── host:8081 (port mapped)    │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Key Principles

- **All traffic forced through proxy** — iptables intercepts at kernel level, no app can bypass
- **Transparent** — apps have no idea they're being proxied, no proxy config needed
- **MITM for HTTPS** — goper generates a root CA, dynamically signs certs per-hostname
- **Sidecar pattern** — goper runs as a separate container sharing browser's network namespace
- **No loop** — goper runs as dedicated user, iptables skips its traffic via owner-matching

---

## Technology Stack

| Component | Library |
|-----------|---------|
| HTTP/HTTPS proxy + MITM | `github.com/elazarl/goproxy` |
| Original dst (redirected) | `golang.org/x/sys/unix` — `SO_ORIGINAL_DST` |
| TLS SNI extraction | `crypto/tls` + `net` (peek ClientHello record) |
| CA & cert generation | `crypto/x509` (stdlib) |
| iptables management | `github.com/coreos/go-iptables` |
| HTTP API | `github.com/go-chi/chi/v5` |
| CLI & config | `github.com/urfave/cli/v2` |
| Logging | `github.com/rs/zerolog` |
| Testing | `github.com/stretchr/testify` |

---

## Project Structure

```
goper/
├── cmd/
│   └── goper/
│       └── main.go                     # Entrypoint, signal handling, orchestration
├── internal/
│   ├── config/
│   │   └── config.go                   # CLI flags, defaults, validation
│   ├── proxy/
│   │   ├── proxy.go                    # goproxy setup, MITM config
│   │   ├── mitm.go                     # CA generation, per-host cert cache
│   │   └── transparent.go              # SO_ORIGINAL_DST + ClientHello SNI parser
│   ├── capture/
│   │   ├── entry.go                    # CapturedEntry struct (request + response)
│   │   ├── capture.go                  # ResponseRecorder, body interception
│   │   └── store.go                    # In-memory ring buffer (configurable cap)
│   ├── api/
│   │   ├── api.go                      # HTTP server, chi router, middleware
│   │   └── handlers.go                 # Endpoint handlers
│   ├── iptables/
│   │   └── iptables.go                 # Install / remove rules on start / stop
│   └── output/
│       └── output.go                   # Output adapter interface (future: file, stdout)
├── Dockerfile                           # Multi-stage build (alpine runtime)
├── docker-compose.yml                   # goper + browser-app
├── go.mod
├── go.sum
├── Makefile                             # build, run, test targets
└── PLAN.md                              # This file
```

---

## Data Flow — HTTPS Interception (most complex case)

```
1. browser-app opens TCP connection to api.example.com:443
2. iptables REDIRECT → connection arrives at goper:8080
3. goper calls SO_ORIGINAL_DST getsockopt → gets original IP:Port
4. goper reads first bytes → detects TLS ClientHello
5. Parses SNI extension → hostname = "api.example.com"
6. Checks cert cache → generates new cert if needed (signed by goper CA)
7. Completes TLS handshake with browser-app (presenting fake cert)
8. browser-app sends HTTP request inside TLS session
9. goper decrypts, logs request, opens real TLS to api.example.com:443
10. Forwards request, receives response
11. Checks Content-Type → if application/json, captures body
12. Stores CapturedEntry in ring buffer
13. Re-encrypts response with browser's session key
14. browser-app receives response, none the wiser
```

### HTTP Case (simpler)

Same as above except:
- No TLS ClientHello to parse
- Destination is determined from HTTP Host header
- No cert generation needed
- Steps 3-7 replaced by: read Host header, determine destination

---

## HTTP API

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/requests` | List captured requests (paginated) |
| `GET` | `/api/requests?since=<timestamp>` | List requests since timestamp |
| `GET` | `/api/requests?method=POST&status=200` | Filter by method/status/url |
| `GET` | `/api/requests/{id}` | Full request + response detail |
| `GET` | `/api/requests/stream` | SSE live feed of new captures |
| `DELETE` | `/api/requests` | Clear all captured data |
| `GET` | `/api/ca.pem` | Download root CA certificate |
| `GET` | `/api/stats` | Stats (total count, bytes captured, uptime) |

### CapturedEntry Structure

```json
{
  "id": "abc123",
  "timestamp": "2026-07-30T12:00:00Z",
  "duration_ms": 342,
  "method": "GET",
  "url": "https://api.example.com/v1/users",
  "scheme": "https",
  "host": "api.example.com",
  "path": "/v1/users",
  "request_headers": {
    "accept": "application/json",
    "authorization": "Bearer ***",
    "user-agent": "Mozilla/5.0 ..."
  },
  "request_body": null,
  "status_code": 200,
  "response_headers": {
    "content-type": "application/json",
    "x-request-id": "req_123"
  },
  "response_body": "{\"users\":[{\"id\":1,\"name\":\"Alice\"}]}",
  "content_type": "application/json"
}
```

Sensitive headers (authorization, cookie, set-cookie) are redacted by default.

---

## Implementation Phases

### Phase 1: Core Proxy + MITM (3-4 days)

- [ ] Initialize Go module, dependencies
- [ ] Implement CA generation (`internal/proxy/mitm.go`)
  - Generate RSA/ECDSA root CA
  - Persist to disk, reload on restart
  - Dynamic per-host cert generation with caching
- [ ] Set up goproxy server (`internal/proxy/proxy.go`)
  - HTTP forward proxy
  - HTTPS CONNECT + MITM
  - Verbose logging
- [ ] CLI entrypoint (`cmd/goper/main.go`)
  - `--port` flag (default 8080)
  - `--api-port` flag (default 8081)
  - `--ca-dir` flag (default `~/.goper/ca`)
  - Signal handling (SIGTERM → graceful shutdown)

**Deliverable**: `goper --port 8080` acts as a manual forward proxy (browser must configure proxy settings). Works for HTTP + HTTPS.

### Phase 2: Transparent Mode + iptables (2-3 days)

- [ ] Implement `SO_ORIGINAL_DST` extraction (`internal/proxy/transparent.go`)
  - Platform check (Linux only)
  - `unix.GetsockoptAny(fd, SOL_IP, SO_ORIGINAL_DST)`
  - Parse returned `unix.RawSockaddrInet4` into IP:Port
- [ ] Implement TLS ClientHello SNI parser
  - Read first bytes, parse TLS record header
  - Extract SNI extension without completing handshake
  - Fallback: resolve original IP to hostname via reverse DNS
- [ ] Implement transparent connection handler
  - Detect HTTP vs HTTPS (peek first byte: `0x16` = TLS, otherwise HTTP)
  - HTTP: read Host header → forward
  - HTTPS: SNI + original dst → generate cert → MITM
- [ ] Implement iptables manager (`internal/iptables/iptables.go`)
  - `Setup()`: install NAT rules (owner-skip + redirect)
  - `Teardown()`: remove rules on shutdown
  - Use `go-iptables` library
  - Run as root or with `CAP_NET_ADMIN`
- [ ] Integrate into main entrypoint

**Deliverable**: `goper --transparent --port 8080` with iptables. Browser in same namespace is automatically intercepted.

### Phase 3: Capture Engine (1-2 days)

- [ ] Define `CapturedEntry` struct (`internal/capture/entry.go`)
- [ ] Implement `ResponseRecorder` (`internal/capture/capture.go`)
  - Wraps `http.ResponseWriter` + `http.Request`
  - Captures status, headers, body
  - Body size limit (configurable, default 1MB)
  - JSON content detection (`Content-Type: application/json`)
- [ ] Implement `RingBuffer` store (`internal/capture/store.go`)
  - Thread-safe via `sync.RWMutex`
  - Configurable capacity (default 10,000 entries)
  - Auto-eviction of oldest entries
  - Methods: `Push`, `Get`, `List`, `Filter`, `Clear`, `Len`
- [ ] Wire capture into proxy handler (both HTTP + HTTPS paths)

**Deliverable**: Intercepted traffic is parsed, JSON bodies extracted, stored in ring buffer.

### Phase 4: HTTP API (2 days)

- [ ] Set up chi router with middleware (`internal/api/api.go`)
  - Logging, panic recovery, CORS, request ID
- [ ] Implement handlers (`internal/api/handlers.go`)
  - `GET /api/requests` — paginated list with optional filters
  - `GET /api/requests/{id}` — single entry detail
  - `GET /api/requests/stream` — SSE using channel subscription pattern
  - `DELETE /api/requests` — clear buffer
  - `GET /api/ca.pem` — serve CA cert for download
  - `GET /api/stats` — ring buffer stats, uptime
- [ ] Add SSE for live feed
  - Store maintains subscriber list
  - New entries pushed to all subscribers
  - Clients reconnect on disconnect

**Deliverable**: Scraper can poll or subscribe to capture data via HTTP API.

### Phase 5: Docker & Packaging (1 day)

- [ ] Multi-stage `Dockerfile`
  - Builder stage: `golang:1.22-alpine`
  - Runtime stage: `alpine:3.19` with `ca-certificates`, `iptables`
  - Copy binary, set `USER goper`
- [ ] `docker-compose.yml`
  - `goper` service with `cap_add: NET_ADMIN`, two networks
  - `browser-app` with `network_mode: "service:goper"`
- [ ] CA cert distribution
  - On first run, goper generates CA and writes to mounted volume
  - User copies to browser-app Dockerfile or bind-mounts
  - Run `update-ca-certificates` in browser-app
- [ ] `Makefile`
  - `make build` — build binary
  - `make docker` — build Docker image
  - `make up` — docker-compose up
  - `make test` — run tests

**Deliverable**: Full Docker setup, one `docker compose up` to run.

### Phase 6: Integration Tests (1-2 days)

- [ ] Unit tests for each internal package
  - Ring buffer concurrency, eviction
  - Cert generation and caching
  - SNI parser (with known test vectors)
  - API handlers
- [ ] Integration test
  - Docker Compose test setup
  - Container makes HTTP + HTTPS requests via curl
  - Verify interception, verify API returns captured data
  - Test CA cert installation
  - Test iptables rule installation/removal

**Deliverable**: CI-ready test suite.

---

## Docker Compose (final)

```yaml
services:
  goper:
    build:
      context: .
      dockerfile: Dockerfile
    cap_add:
      - NET_ADMIN
    user: goper
    networks:
      - intercepted
      - upstream
    ports:
      - "8081:8081"  # HTTP API exposed to host
    volumes:
      - goper-ca:/home/goper/.goper/ca
      - goper-data:/home/goper/.goper/data
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8081/api/stats"]
      interval: 5s
      retries: 5

  browser-app:
    build: ./browser  # your scraper image
    network_mode: "service:goper"
    depends_on:
      goper:
        condition: service_healthy
    volumes:
      - ./ca-cert.pem:/usr/local/share/ca-certificates/goper.crt
    # No proxy settings needed. iptables handles everything.
    # If the app needs to make direct connections (no intercept), use a different user.

networks:
  intercepted:
    driver: bridge
  upstream:
    driver: bridge

volumes:
  goper-ca:
  goper-data:
```

---

## CA Certificate Management

```
First run:
  goper/                   browser-app/
    │                         │
    ├── Generate root CA      │
    ├── Save to:              │
    │  /home/goper/.goper/ca/ │
    │  ├── ca-key.pem         │
    │  └── ca-cert.pem        │
    │                         │
    ├── Serve via API:        │
    │  GET /api/ca.pem        │
    │                         │
    └── User downloads        │
        and copies into       ├── ca-cert.pem ← bind mount or COPY
        browser-app Dockerfile│
                              ├── RUN update-ca-certificates

Subsequent runs:
  goper reloads persisted CA  →  same CA used  →  same fake certs
  No need to reinstall in browser-app
```

---

## Edge Cases & Considerations

| Concern | Mitigation |
|---------|------------|
| **TLS 1.3** | goproxy supports TLS 1.3; verify compatibility |
| **HTTP/2** | goproxy handles HTTP/2 → HTTP/1.1 downgrade; capture in HTTP/1.1 format |
| **WebSocket** | Capture upgrade headers but don't proxy WebSocket frames (v1) |
| **Large responses** | Configurable body size limit (default 1MB), truncated bodies flagged |
| **Binary responses** | Detect non-UTF8, store as base64 or skip based on Content-Type |
| **CONNECT tunnels (non-HTTP)** | Pass through without interception |
| **QUIC / HTTP/3** | Not supported (UDP). iptables only redirects TCP. Future enhancement. |
| **Multiple browser-app containers** | Each needs its own goper sidecar (or share one goper per namespace) |
| **goper restart** | Ring buffer is lost (in-memory). Persist to file if needed (Phase 7+) |
| **iptables race condition** | goper installs rules before marking healthy; browser-app waits for healthy |
| **Cert expiration** | Certs generated with 1-year expiry; regenerate on demand |

---

## Future Enhancements (post-v1)

- [ ] Response modification (inject headers, rewrite bodies)
- [ ] Request replay (resend captured requests)
- [ ] Persistent storage (SQLite or file-based)
- [ ] Web UI dashboard for browsing captured traffic
- [ ] WebSocket frame capture
- [ ] gRPC support (HTTP/2 proto-aware capture)
- [ ] QUIC / HTTP/3 interception (requires different approach)
- [ ] Distributed capture → central aggregator
- [ ] Filtering rules (only capture URLs matching pattern)
