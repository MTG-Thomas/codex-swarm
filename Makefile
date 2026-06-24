.PHONY: all build test vet fmt fmt-check vulncheck check clean

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

clean:
	$(GO) clean
	rm -rf $(BIN_DIR)
