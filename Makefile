BINARY := bin/skillctl
PREFIX ?= $(HOME)/.local

.PHONY: build install clean vet

build:
	go build -o $(BINARY) ./cmd/skillctl

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BINARY) $(PREFIX)/bin/skillctl

vet:
	go vet ./...

clean:
	rm -rf bin dist
