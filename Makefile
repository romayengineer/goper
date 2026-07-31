BINARY=goper
BUILD_DIR=.
CMD_DIR=./cmd/goper

.PHONY: build docker up down test cover clean

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

test:
	go test ./... -v

cover:
	go test ./... -coverprofile=coverage.out
	@go tool cover -func=coverage.out | grep total | awk '{print "total: " $$3}'
	@rm -f coverage.out

lint:
	go vet ./...

clean:
	rm -f $(BINARY)
