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

# Load generator defaults, overridable: make loadgen RATE=50000 TOPICS=2000
RATE     ?= 10000
TOPICS   ?= 500
PAYLOAD  ?= 256
DURATION ?= 60s
PATTERN  ?= steady

.PHONY: build run dev test test-int test-short bench golden lint fmt vuln certs loadgen snapshot release-check demo clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

run: build
	./bin/$(BINARY) -b $(BROKER)

dev:
	docker compose -f deploy/docker-compose.yml up -d
	$(MAKE) run

test:
	go test -race ./...

# Skips the minute-long memory ceiling test and the retained flood.
test-short:
	go test -short -race ./...

test-int:
	docker compose -f deploy/docker-compose.yml up -d
	go test -tags=integration -race ./test/... ; status=$$? ; \
	docker compose -f deploy/docker-compose.yml down ; exit $$status

bench:
	go test ./internal/ui ./internal/store -run XXX -bench . -benchmem

# Regenerate the golden frames. Read the diff before committing it.
golden:
	go test ./internal/ui -update

# Pinned so `make lint` and CI cannot disagree about what a lint failure is.
# Keep in step with .github/workflows/ci.yml.
GOLANGCI := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1
GOFUMPT  := mvdan.cc/gofumpt@latest
GOVULN   := golang.org/x/vuln/cmd/govulncheck@latest

# Run through `go run` rather than expecting the binaries on PATH: a `make
# lint` that prints "command not found" and keeps going is how four lint
# failures reached CI unnoticed.
lint:
	go run $(GOLANGCI) run
	@out=$$(go run $(GOFUMPT) -l .) ; \
	if [ -n "$$out" ]; then echo "not gofumpt-formatted:" ; echo "$$out" ; exit 1 ; fi

fmt:
	go run $(GOFUMPT) -w .

vuln:
	go run $(GOVULN) ./...

certs:
	./deploy/certs/gen.sh

loadgen:
	go run ./cmd/mqttload --broker $(BROKER) --rate $(RATE) --topics $(TOPICS) \
		--payload $(PAYLOAD) --duration $(DURATION) --pattern $(PATTERN)

# Records the README demo against the dev broker. Needs vhs (which needs ttyd
# and ffmpeg) and the compose stack up, because the tape drives the real binary
# against the real seeded tree rather than faking data.
#
# The broker check is not ceremony: with nothing listening the tape records a
# perfectly good GIF of an empty app stuck on "reconnecting…", and you do not
# find out until you open the result.
demo: build
	@docker compose -f deploy/docker-compose.yml exec -T mosquitto \
		mosquitto_pub -h localhost -t vhs/ready -m ok >/dev/null 2>&1 || { \
		echo "the dev broker is not answering on 1883."; \
		echo "run: make certs && docker compose -f deploy/docker-compose.yml up -d"; \
		echo "(without certs the broker cannot load ca.pem and exits at startup)"; \
		exit 1; }
	vhs docs/demo.tape

snapshot:
	goreleaser release --snapshot --clean

# Validates .goreleaser.yaml without building anything. Cheap enough to run
# before every push that touches it; CI proves the rest with release-dry-run.
release-check:
	goreleaser check

clean:
	rm -rf bin dist
