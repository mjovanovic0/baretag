# The build that runs on the server and is embedded in the ISO.
BINARY   := dist/baretag-linux-amd64
# The build for this workstation, used to prepare images and decode scans.
HOST     := dist/baretag
ISO      ?=
OUT      ?= baretag.iso
KARGS    ?=
GO       ?= go

.PHONY: all build host test lint iso clean

all: build host

## the linux/amd64 build that runs on the server and is baked into the ISO
build:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/baretag

## a build for this workstation, for decoding scans and for baking ISOs
host:
	mkdir -p dist
	$(GO) build -trimpath -o $(HOST) ./cmd/baretag

test:
	$(GO) test ./...

## the full test run, including the ones that need a real CoreOS ISO
test-iso: 
	@test -n "$(ISO)" || { echo "usage: make test-iso ISO=/path/to/rhcos-live.iso"; exit 1; }
	BARETAG_TEST_ISO=$(abspath $(ISO)) $(GO) test ./... -count=1

lint:
	gofmt -l . | tee /dev/stderr | (! read)
	$(GO) vet ./...

## bake the tool into a CoreOS live ISO: make iso ISO=rhcos-live.iso
iso: build host
	@test -n "$(ISO)" || { echo "usage: make iso ISO=/path/to/rhcos-live.iso [OUT=inventory.iso]"; exit 1; }
	$(HOST) bake-into $(ISO) -o $(OUT) --binary $(BINARY) --force \
		$(foreach k,$(KARGS),--karg $(k))

clean:
	rm -rf dist
