GO       ?= go
EXE      := $(shell $(GO) env GOEXE)
BINARY   := vigil$(EXE)
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
SIZE     ?= 150x44
SHOTS    ?= docs/screenshots
TABS     := overview servers detail alerts settings

.PHONY: all build install test race cover lint vet fmt fmt-check run-demo screenshots cross clean help

all: lint test build

build: ## Build ./vigil with the version stamped in
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/vigil

install: ## Install vigil into $GOBIN
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/vigil

test: ## Run all tests
	$(GO) test ./...

race: ## Run all tests with the race detector (needs cgo)
	$(GO) test -race ./...

cover: ## Write coverage.out and print a summary
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -n 1

lint: vet fmt-check ## go vet and gofmt check

vet:
	$(GO) vet ./...

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed for:"; echo "$$out"; exit 1; fi

fmt: ## Format all Go files
	gofmt -w .

run-demo: ## Run the dashboard with a synthetic fleet
	$(GO) run ./cmd/vigil demo

screenshots: build ## Render every tab to $(SHOTS)/<tab>.ans (ANSI text)
	mkdir -p $(SHOTS)
	for tab in $(TABS); do ./$(BINARY) render --size $(SIZE) --tab $$tab > $(SHOTS)/$$tab.ans; done
	./$(BINARY) render --size $(SIZE) --compat ascii > $(SHOTS)/overview-ascii.ans

cross: ## Check that linux, darwin and windows builds compile
	GOOS=linux   GOARCH=amd64 $(GO) build -o /dev/null ./...
	GOOS=linux   GOARCH=arm64 $(GO) build -o /dev/null ./...
	GOOS=darwin  GOARCH=arm64 $(GO) build -o /dev/null ./...
	GOOS=windows GOARCH=amd64 $(GO) build -o /dev/null ./...

clean:
	rm -f vigil vigil.exe coverage.out

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "%-12s %s\n", $$1, $$2}'
