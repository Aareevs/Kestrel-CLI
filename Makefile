# Kestrel developer tasks. `make check` runs every quality gate.

PKG      := github.com/kestrel-cli/kestrel
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w -X $(PKG)/internal/buildinfo.Version=$(VERSION) -X $(PKG)/internal/buildinfo.Commit=$(COMMIT) -X $(PKG)/internal/buildinfo.Date=$(DATE)
STATICCHECK_VERSION ?= 2025.1.1

.PHONY: all build install test race vet fmt fmt-check lint cross check golden clean

all: check

build:
	go build -ldflags "$(LDFLAGS)" -o bin/kestrel ./cmd/kestrel

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/kestrel

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# Runs staticcheck without installing it globally.
lint:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

cross:
	GOOS=linux   GOARCH=amd64 go build -o /dev/null ./cmd/kestrel
	GOOS=linux   GOARCH=arm64 go build -o /dev/null ./cmd/kestrel
	GOOS=darwin  GOARCH=amd64 go build -o /dev/null ./cmd/kestrel
	GOOS=darwin  GOARCH=arm64 go build -o /dev/null ./cmd/kestrel
	GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/kestrel

check: fmt-check vet test race build cross

# Regenerate golden files after an intentional output change; review the diff.
golden:
	go test ./tests/integration -update

clean:
	rm -rf bin dist
