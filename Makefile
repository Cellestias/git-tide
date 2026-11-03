.PHONY: build install release test lint vuln fmt

PREFIX ?= /usr/local
VERSION ?= v1-dev+$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/cellestias/git-tide/internal/cli.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o tide ./cmd/tide

install: build
	install -d $(PREFIX)/bin
	install -m 755 tide $(PREFIX)/bin/tide

# make release VERSION=v1.2.3: archives and checksums for every platform in ./dist
release:
	./scripts/release.sh $(VERSION)

test:
	go test -race ./...

fmt:
	gofmt -w .

lint:
	go vet ./...
	golangci-lint run

vuln:
	govulncheck ./...
