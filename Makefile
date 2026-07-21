.PHONY: all build test vet fmt fmt-check vulncheck check windows-resources clean

GO ?= go
BIN_DIR ?= bin

all: check build

build:
	$(GO) build -trimpath -o $(BIN_DIR)/cs ./cmd/cs
	$(GO) build -trimpath -o $(BIN_DIR)/csd ./cmd/csd

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

fmt-check:
	test -z "$$(gofmt -l .)"

vulncheck:
	govulncheck ./...

check: fmt-check vet test

windows-resources:
	@test -n "$(VERSION)" || (echo "VERSION is required, for example make windows-resources VERSION=0.4.1" >&2; exit 1)
	$(GO) run ./scripts/generate-windows-resources.go -version "$(VERSION)"

clean:
	$(GO) clean
	rm -rf $(BIN_DIR)
	rm -f cmd/cs/resource_windows_*.syso cmd/csd/resource_windows_*.syso
