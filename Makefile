BINARY=goper
BUILD_DIR=.
CMD_DIR=./cmd/goper

.PHONY: build docker up down test test-unit test-integration test-e2e test-all cover cover-integration clean

build:
	go build -ldflags="-s -w" -o $(BINARY) $(CMD_DIR)

run:
	go run $(CMD_DIR) --verbose

docker:
	docker compose build

up:
	docker compose up -d

down:
	docker compose down

# unit tests only (integration tests excluded via build tag)
test:
	go test ./... -v

test-unit:
	go test ./... -race -count=1

# integration tests only (loopback network / full pipeline)
test-integration:
	go test -tags=integration ./... -race -count=1 -v

# everything
test-all:
	go test -tags=integration ./... -race -count=1

# full docker-compose workflow (goper + windowed Chrome + Playwright)
test-e2e:
	go test -tags=e2e ./test/e2e -v -count=1 -timeout 20m

# unit coverage
cover:
	go test ./... -coverprofile=coverage.out
	@go tool cover -func=coverage.out | grep total | awk '{print "total: " $$3}'
	@rm -f coverage.out

# combined coverage (unit + integration)
cover-integration:
	go test -tags=integration ./... -coverprofile=coverage.out
	@go tool cover -func=coverage.out | grep total | awk '{print "total: " $$3}'
	@rm -f coverage.out

lint:
	go vet ./...

clean:
	rm -f $(BINARY)
