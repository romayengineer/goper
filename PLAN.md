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

> Phase 1 (Core Proxy + MITM) is fully implemented and has been removed.
> The remaining items below are the parts of each phase not yet implemented.

### Phase 2: Transparent Mode + iptables

- [ ] SNI parser fallback: resolve original IP to hostname via reverse DNS (currently falls back to the raw IP)
- [ ] Use `go-iptables` library (currently shells out to the `iptables` binary)

### Phase 3: Capture Engine

- [ ] `ResponseRecorder` wraps `http.ResponseWriter` + `http.Request` (currently captures from `*http.Response` in the response handler)
- [ ] Configurable body size limit (currently hardcoded 1MB)

### Phase 4: HTTP API

- [ ] Request-ID middleware
- [ ] `GET /api/stats` — ring buffer stats + uptime (currently returns count only)

### Phase 5: Docker & Packaging

- [ ] `goper` service with `cap_add: NET_ADMIN` and two networks (`intercepted`, `upstream`)
- [ ] `browser-app` with `network_mode: "service:goper"` (currently shares a bridge network with explicit `--proxy-server`)

### Phase 6: Integration Tests

- [ ] Container makes HTTP + HTTPS requests via curl
- [ ] Test iptables rule installation/removal in a real container

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
