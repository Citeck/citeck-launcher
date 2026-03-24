VERSION ?= dev
BINARY  := citeck
GOFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build build-fast test test-unit test-integration lint clean

build:
	go build $(GOFLAGS) -o $(BINARY) ./cmd/citeck

build-fast:
	go build $(GOFLAGS) -o $(BINARY) ./cmd/citeck

test:
	go test ./...

test-unit:
	go test ./internal/...

test-integration:
	go test -tags=integration ./tests/...

lint:
	golangci-lint run

clean:
	rm -f $(BINARY)
