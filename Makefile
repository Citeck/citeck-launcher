VERSION ?= dev
BINARY  := citeck
DESKTOP := citeck-desktop
GO_BUILD_FLAGS := -ldflags "-X main.version=$(VERSION)"
WEBDIST := internal/daemon/webdist

.PHONY: build build-fast build-web build-desktop test test-unit test-integration lint clean dev-daemon dev-desktop

build: build-web
	go build $(GO_BUILD_FLAGS) -o $(BINARY) ./cmd/citeck

build-fast:
	@mkdir -p $(WEBDIST)
	@test -f $(WEBDIST)/index.html || echo '<html><body>Run "make build" to include web UI</body></html>' > $(WEBDIST)/index.html
	go build $(GO_BUILD_FLAGS) -o $(BINARY) ./cmd/citeck

build-web:
	cd web && npm run build
	rm -rf $(WEBDIST)
	cp -r web/dist $(WEBDIST)

build-desktop: build-web
	CGO_ENABLED=1 go build $(GO_BUILD_FLAGS) -o $(DESKTOP) ./cmd/citeck-desktop

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

dev-desktop:
	cd web && npm run build
	CGO_ENABLED=1 go build -o $(DESKTOP) ./cmd/citeck-desktop
	./$(DESKTOP)

clean:
	rm -f $(BINARY) $(DESKTOP)
	rm -rf $(WEBDIST)
	rm -rf web/dist
