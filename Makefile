.PHONY: build test check install

build:
	go build -trimpath -ldflags='-s -w' -o bin/slack-hours ./cmd/slack-hours

test:
	go test -race ./...
	node --test test/*guard.test.mjs

check:
	go vet ./...
	$(MAKE) test

install: build
	./bin/slack-hours install
