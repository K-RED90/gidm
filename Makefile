GO      ?= go
BIN_DIR ?= bin
PKGS    ?= ./...
LDFLAGS ?= -s -w

# NOWORK keeps the core module's build/test/lint/vuln independent of the desktop
# Wails module that go.work adds: the pure-Go, no-CGO core is never dragged
# through the CGO/webview desktop tree (e.g. on CI runners without a webview
# toolchain). The desktop-* targets deliberately omit it so they resolve the
# local api package through the workspace.
NOWORK  := GOWORK=off

.PHONY: all build test bench vet fmt tidy lint vuln clean check \
	desktop-dev desktop-build desktop-test desktop-generate

all: build

build:
	$(NOWORK) $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/gidmd     ./cmd/gidmd
	$(NOWORK) $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/gidm      ./cmd/gidm
	$(NOWORK) $(GO) build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/gidm-host ./cmd/gidm-host

test:
	$(NOWORK) $(GO) test -race -count=1 $(PKGS)

bench:
	$(NOWORK) $(GO) test -run='^$$' -bench=. -benchmem $(PKGS)

vet:
	$(NOWORK) $(GO) vet $(PKGS)

fmt:
	gofmt -l -w .

tidy:
	$(NOWORK) $(GO) mod tidy

lint:
	$(NOWORK) golangci-lint run

vuln:
	$(NOWORK) govulncheck $(PKGS)

clean:
	rm -rf $(BIN_DIR)

check: vet test

# Desktop app (Wails v3 + Svelte + Bun). Opt-in, never part of `all`/`check`, and
# without NOWORK so the workspace resolves the local api package. Requires wails3
# (go install github.com/wailsapp/wails/v3/cmd/wails3@latest) and bun on PATH.
desktop-dev:
	cd desktop && wails3 dev

desktop-build:
	cd desktop && wails3 build

desktop-test:
	cd desktop && $(GO) test -race ./bridge/... ./internal/...

desktop-generate:
	cd desktop && wails3 generate bindings -ts
