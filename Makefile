BINARY=goper
BUILD_DIR=.
CMD_DIR=./cmd/goper

.PHONY: build docker up down test clean

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

lint:
	go vet ./...

clean:
	rm -f $(BINARY)
