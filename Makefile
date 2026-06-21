GO      ?= go
BIN_DIR ?= bin
PKGS    ?= ./...
LDFLAGS ?= -s -w

.PHONY: all build test bench vet fmt tidy lint vuln clean check

all: build

build:
	$(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/gidmd     ./cmd/gidmd
	$(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/gidm      ./cmd/gidm
	$(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/gidm-host ./cmd/gidm-host

test:
	$(GO) test -race -count=1 $(PKGS)

bench:
	$(GO) test -run='^$$' -bench=. -benchmem $(PKGS)

vet:
	$(GO) vet $(PKGS)

fmt:
	gofmt -l -w .

tidy:
	$(GO) mod tidy

lint:
	golangci-lint run

vuln:
	govulncheck $(PKGS)

clean:
	rm -rf $(BIN_DIR)

check: vet test
