BINDIR ?= /usr/local/bin
BINARIES := gfw-watchdog echo-server

.PHONY: all build install test clean

all: build

build:
	CGO_ENABLED=0 go build -v -trimpath -mod=readonly -ldflags="-s -w -buildid=" -o gfw-watchdog ./cmd/gfw-watchdog
	CGO_ENABLED=0 go build -v -trimpath -mod=readonly -ldflags="-s -w -buildid=" -o echo-server ./cmd/echo-server

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 $(BINARIES) $(DESTDIR)$(BINDIR)

test:
	go vet ./...
	go test ./...

clean:
	go clean -cache -testcache
	rm -f $(BINARIES)
