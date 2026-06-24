.PHONY: all build test vet fmt check clean

GO ?= go
BIN_DIR ?= bin

all: check build

build:
	$(GO) build -o $(BIN_DIR)/cs ./cmd/cs
	$(GO) build -o $(BIN_DIR)/csd ./cmd/csd

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

check: fmt vet test

clean:
	$(GO) clean
	rm -rf $(BIN_DIR)
