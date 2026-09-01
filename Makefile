GO ?= go
BINARY ?= bin/grove
COMMAND := ./cmd/grove
STATICCHECK_VERSION ?= v0.7.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: all build install fmt fmt-check vet staticcheck test test-race license-check check cross-build clean

all: build

build:
	@mkdir -p "$(dir $(BINARY))"
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o "$(BINARY)" $(COMMAND)

install:
	CGO_ENABLED=0 $(GO) install -ldflags "-X main.version=$(VERSION)" $(COMMAND)

fmt:
	@find . -type f -name '*.go' -not -path './vendor/*' -exec gofmt -w {} +

fmt-check:
	@unformatted="$$(find . -type f -name '*.go' -not -path './vendor/*' -exec gofmt -l {} +)"; \
	if [ -n "$$unformatted" ]; then printf '%s\n' "$$unformatted"; exit 1; fi

vet:
	$(GO) vet ./...

staticcheck:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

license-check:
	@grep -Fxq 'MIT License' LICENSE
	@grep -Eq '^Copyright \(c\) [0-9]{4} .+' LICENSE
	@grep -Fq 'Permission is hereby granted, free of charge, to any person obtaining a copy' LICENSE
	@grep -Fq 'The above copyright notice and this permission notice shall be included in all' LICENSE
	@grep -Fq 'THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND' LICENSE
	@grep -Fq 'AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER' LICENSE

check: fmt-check vet test-race license-check build

cross-build:
	@mkdir -p dist
	@set -eu; \
	for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do \
		os="$${target%/*}"; \
		arch="$${target#*/}"; \
		printf 'Building %s/%s\n' "$$os" "$$arch"; \
		CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" $(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o "dist/grove-$$os-$$arch" $(COMMAND); \
	done
	cp LICENSE THIRD_PARTY_NOTICES.md dist/
	mkdir -p dist/skills/grove-worktrees
	cp skills/grove-worktrees/SKILL.md dist/skills/grove-worktrees/

clean:
	rm -rf bin dist coverage.out
