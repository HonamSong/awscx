BINARY := awscx-go
PKG    := ./cmd/awscx-go
DIST   := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

GO_ENV := CGO_ENABLED=0

.PHONY: build run tidy fmt vet test clean release

build:
	$(GO_ENV) go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) $(PKG)

run:
	go run $(PKG)

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

clean:
	rm -rf $(DIST)

release: clean
	$(GO_ENV) GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64   $(PKG)
	$(GO_ENV) GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64   $(PKG)
	$(GO_ENV) GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64  $(PKG)
	$(GO_ENV) GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64  $(PKG)