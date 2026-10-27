.PHONY: build test check install release

VERSION ?= dev

build:
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/slack-hours ./cmd/slack-hours

test:
	go test -race ./...
	node --test test/*guard.test.mjs

check:
	go vet ./...
	$(MAKE) test

install: build
	./bin/slack-hours install

release:
	./scripts/package-release "$(VERSION)"
