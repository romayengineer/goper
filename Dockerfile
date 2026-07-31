FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /goper ./cmd/goper/

FROM alpine:3.21

RUN apk add --no-cache \
    ca-certificates \
    iptables \
    ip6tables

RUN mkdir -p /home/goper/.goper/ca

COPY --from=builder /goper /usr/local/bin/goper

COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

EXPOSE 8080 8081

ENTRYPOINT ["/docker-entrypoint.sh"]
