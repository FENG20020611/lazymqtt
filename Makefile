BINARY  := lazymqtt
PKG     := github.com/Onizuka893/lazymqtt
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

BROKER ?= tcp://localhost:1883

.PHONY: build run dev test test-int lint fmt vuln certs loadgen snapshot clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

run: build
	./bin/$(BINARY) -b $(BROKER)

dev:
	docker compose -f deploy/docker-compose.yml up -d
	$(MAKE) run

test:
	go test -race ./...

test-int:
	docker compose -f deploy/docker-compose.yml up -d
	go test -tags=integration -race ./test/... ; status=$$? ; \
	docker compose -f deploy/docker-compose.yml down ; exit $$status

lint:
	golangci-lint run
	@test -z "$$(gofumpt -l . 2>/dev/null)" || (gofumpt -l . && exit 1)

fmt:
	gofumpt -w .

vuln:
	govulncheck ./...

certs:
	./deploy/certs/gen.sh

loadgen:
	go run ./cmd/mqttload --rate $(or $(RATE),10000)

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
