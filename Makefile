VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test run install uninstall release

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/komar ./cmd/komar

test:
	go vet ./...
	go test ./...

run: build
	./bin/komar

install:
	./install.sh

uninstall:
	./install.sh --uninstall

release:
	mkdir -p dist
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/komar ./cmd/komar && \
		tar -czf dist/komar-linux-$$arch.tar.gz -C dist komar && rm dist/komar; \
	done
