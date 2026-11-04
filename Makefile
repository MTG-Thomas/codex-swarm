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

.PHONY: secret-scan pre-push
# Local authenticated gate; keep CI/check/test targets unchanged.
secret-scan: secret-gate-python
	"$(SECRET_GATE_PYTHON)" -I scripts/secret_gate.py

pre-push: secret-gate-python
	"$(SECRET_GATE_PYTHON)" -I scripts/secret_gate.py --pre-push

# Native Windows: set MTG_SECRET_GATE_PYTHON to an approved absolute interpreter.
PYTHON ?= python3

.PHONY: secret-gate-python
ifeq ($(OS),Windows_NT)
SECRET_GATE_PYTHON ?= $(MTG_SECRET_GATE_PYTHON)
else
SECRET_GATE_PYTHON ?= $(PYTHON)
endif

secret-gate-python:
ifeq ($(OS),Windows_NT)
	$(if $(or $(findstring :/,$(subst \,/,$(SECRET_GATE_PYTHON))),$(filter //%,$(subst \,/,$(SECRET_GATE_PYTHON)))),,$(error UNAVAILABLE: set MTG_SECRET_GATE_PYTHON to an approved absolute Python path outside all Git checkouts; publication blocked))
endif
