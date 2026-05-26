VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS  = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build build-core build-web test lint fmt dev clean deps dist dist-all check-release

build: build-core

build-core: build-web
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/yipyap ./cmd/yipyap

build-web:
	cd web && npm ci && npm run build

test:
	go test ./... -v -race

lint:
	go vet ./...
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, skipping"

fmt:
	go fmt ./...

dev:
	go run ./cmd/yipyap

deps:
	go mod download
	go mod tidy

dist: build-web
	goreleaser build --snapshot --clean

dist-all: build-web
	goreleaser release --snapshot --clean

check-release:
	goreleaser check

clean:
	rm -rf bin/ dist/
