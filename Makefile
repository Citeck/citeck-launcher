VERSION ?= dev
BINARY  := citeck
GOFLAGS := -ldflags "-X main.version=$(VERSION)"
WEBDIST := internal/daemon/webdist

.PHONY: build build-fast build-web test test-unit test-integration lint clean

build: build-web
	go build $(GOFLAGS) -o $(BINARY) ./cmd/citeck

build-fast:
	@mkdir -p $(WEBDIST)
	@test -f $(WEBDIST)/index.html || echo '<html><body>Run "make build" to include web UI</body></html>' > $(WEBDIST)/index.html
	go build $(GOFLAGS) -o $(BINARY) ./cmd/citeck

build-web:
	cd web && npm run build
	rm -rf $(WEBDIST)
	cp -r web/dist $(WEBDIST)

test:
	go test -race ./...
	cd web && npm test

test-unit:
	go test -race ./internal/...

test-integration:
	go test -tags=integration ./tests/...

lint:
	golangci-lint run
	cd web && npm run lint

dev-daemon:
	go run ./cmd/citeck start --foreground &
	cd web && npm run dev

clean:
	rm -f $(BINARY)
	rm -rf $(WEBDIST)
	rm -rf web/dist
