BINARY  := merapi
VERSION := 0.1.0
LDFLAGS := -X github.com/bilhasry/merapi/internal/cli.Version=$(VERSION)

# Go binary: prefer PATH, fall back to the common ~/.local/go install.
GO := $(shell command -v go 2>/dev/null || echo $(HOME)/.local/go/bin/go)

.PHONY: build install test vet fmt clean demo example

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

install:
	$(GO) install -ldflags "$(LDFLAGS)" ./cmd/$(BINARY)
	@BINDIR="$$($(GO) env GOBIN)"; [ -z "$$BINDIR" ] && BINDIR="$$($(GO) env GOPATH)/bin"; \
	case ":$$PATH:" in *":$$BINDIR:"*) echo "✔ $(BINARY) installed to $$BINDIR (on PATH)";; \
	*) echo "✔ $(BINARY) installed to $$BINDIR"; \
	   echo "  NOTE: $$BINDIR is not on your PATH — add this to your shell profile:"; \
	   echo "    export PATH=\"$$BINDIR:\$$PATH\"";; esac

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w cmd internal

clean:
	rm -rf bin dist

# Quick local playground: builds, then exercises the CLI in ./demo
demo: build
	rm -rf demo
	mkdir -p demo
	cd demo && ../bin/$(BINARY) init --title "Demo API" --server http://localhost:8080
	cd demo && ../bin/$(BINARY) schema add Pet --property id:integer --property name:string --required id,name
	cd demo && ../bin/$(BINARY) endpoint add GET /pets --summary "List pets" --description "Returns all pets." --response 200:OK:Pet
	cd demo && ../bin/$(BINARY) validate
	@echo "\nRun tests against a live API with: cd demo && ../bin/$(BINARY) test run"

# End-to-end example: serve the wilayah-api mock and run its test scenarios
example: build
	@sh -c 'cd examples/wilayah-api && exec $(CURDIR)/bin/$(BINARY) mock --addr 127.0.0.1:9292 --quiet >/dev/null 2>&1 & \
	MOCK_PID=$$!; sleep 1; \
	$(CURDIR)/bin/$(BINARY) -C examples/wilayah-api --env local test run; STATUS=$$?; \
	kill $$MOCK_PID 2>/dev/null; exit $$STATUS'
