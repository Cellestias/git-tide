.PHONY: build test lint vuln fmt

build:
	go build -o tide ./cmd/tide

test:
	go test -race ./...

fmt:
	gofmt -w .

lint:
	go vet ./...
	golangci-lint run

vuln:
	govulncheck ./...
