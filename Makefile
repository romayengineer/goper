BINARY=goper
BUILD_DIR=.
CMD_DIR=./cmd/goper

.PHONY: build docker up down test test-unit test-integration test-e2e test-e2e-mac test-e2e-linux test-all cover cover-integration setup-mac setup-linux clean

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

# macOS-only: open a real Chrome window on the Mac (via XQuartz) and run the workflow
test-e2e-mac:
	go test -tags=e2e ./test/e2e -run TestComposeChromeWorkflowMacWindow -v -count=1 -timeout 20m

# Linux-only: open a real Chrome window on the local X display and run the workflow
test-e2e-linux:
	go test -tags=e2e ./test/e2e -run TestComposeChromeWorkflowLinuxWindow -v -count=1 -timeout 20m

# one-time macOS setup for the Chrome window (XQuartz)
setup-mac:
	@echo "One-time macOS setup for the Chrome window:"
	@echo "  brew install --cask xquartz"
	@echo "  defaults write org.xquartz.X11 nolisten_tcp -bool false"
	@echo "  open -a XQuartz"
	@echo "  xhost +"

# per-session Linux setup: allow the container to connect to the X display
setup-linux:
	@echo "Allow Docker containers to reach your X display (per session):"
	@echo "  xhost +local:"

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
