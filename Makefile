# cinefin-playout task runner. Same verbs as cinefin's Makefile so both repos
# drive identically: setup / lint / format / test / build / check / release.
#
# Everything is pure Go with CGO off. The macOS tray needs cgo (Cocoa) and is
# built on a Mac by hand (scripts/build-macos-app.sh), so it is not part of the
# cross-compile gate. CI installs Go, then runs `make check` — the same steps you
# run locally.
.DEFAULT_GOAL := help
.PHONY: help setup lint format build test check cross release clean

BIN := cinefin-playout
PKG := ./cmd/cinefin-playout

# Cross-compile gate (compile-time check for the os-tagged files: unix sockets vs
# named pipes). The service variant covers every OS/arch; the desktop (-tags ui)
# variant covers linux/windows — darwin's tray is cgo, built on a Mac.
CROSS_SVC := linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64
CROSS_UI  := linux/amd64 linux/arm64 windows/amd64 windows/arm64

help: ## List targets
	@grep -hE '^[a-z][a-z-]*:.*##' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*## "}{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

setup: ## Download Go module dependencies
	go mod download

lint: ## gofmt check + go vet (both build variants)
	@unformatted="$$(gofmt -l .)"; \
		if [ -n "$$unformatted" ]; then echo "gofmt needed on:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	CGO_ENABLED=0 go vet -tags ui ./...

format: ## gofmt -w the whole tree
	gofmt -w .

build: ## Build the agent for the host (-tags ui) -> ./$(BIN)
	CGO_ENABLED=0 go build -tags ui -o $(BIN) $(PKG)

test: ## Run the Go test suite
	go test ./...

cross: ## Cross-compile every release target, both variants (compile gate)
	@for t in $(CROSS_SVC); do \
		echo "--- service $$t"; \
		CGO_ENABLED=0 GOOS=$${t%/*} GOARCH=$${t#*/} go build ./... || exit 1; \
	done
	@for t in $(CROSS_UI); do \
		echo "--- desktop $$t"; \
		CGO_ENABLED=0 GOOS=$${t%/*} GOARCH=$${t#*/} go build -tags ui ./... || exit 1; \
	done

check: lint test cross ## Everything CI runs: lint + tests + cross-compile

release: ## Tag + push a release (private/Gitea side): make release VERSION=vX.Y.Z
	scripts/release.sh $(VERSION)

clean: ## Remove build artifacts
	rm -rf build dist $(BIN) $(BIN).exe
