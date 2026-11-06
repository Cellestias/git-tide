.PHONY: build install release readme-links test lint vuln fmt

PREFIX ?= /usr/local
VERSION ?= v1-dev+$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/cellestias/git-tide/internal/cli.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o gtide ./cmd/gtide

install: build
	install -d $(PREFIX)/bin
	install -m 755 gtide $(PREFIX)/bin/gtide
	ln -sf gtide $(PREFIX)/bin/tide   # optional short name

# make release VERSION=v1.2.3: archives and checksums for every platform in ./dist
release:
	./scripts/release.sh $(VERSION)

# make readme-links VERSION=v1.2.3: point the README download table at that release
readme-links:
	@case "$(VERSION)" in v[0-9]*) ;; *) echo "usage: make readme-links VERSION=vMAJOR.MINOR.PATCH" >&2; exit 2 ;; esac
	perl -pi -e 's#(releases/download/)v[0-9][^/]*/#$$1$(VERSION)/#g; s#([/\[]gtide_)v[0-9][^_]*(_)#$$1$(VERSION)$$2#g; s#(\$$version = ")v[0-9][^"]*(")#$$1$(VERSION)$$2#; s#(\*\*)v[0-9][0-9.]*(\*\*, the current release)#$$1$(VERSION)$$2#' README.md

test:
	go test -race ./...

fmt:
	gofmt -w .

lint:
	go vet ./...
	golangci-lint run

vuln:
	govulncheck ./...
