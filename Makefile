BIN      := tiup-backup
PKG      := ./cmd/tiup-backup
DIST     := dist
PLATFORMS := linux/amd64 linux/arm64

.PHONY: all build test vet fmt package clean

all: vet test build

## build: binary for the current platform into dist/
build:
	@mkdir -p $(DIST)
	go build -trimpath -o $(DIST)/$(BIN) $(PKG)

## test: unit tests
test:
	go test ./...

vet:
	go vet ./...
	GOOS=linux go vet ./...

fmt:
	gofmt -l -w .

## package: dist/tiup-backup-<os>-<arch>.tar.gz (+ .sha256), each holding the binary + install.sh.
## Upload the .tar.gz and .sha256 files as the assets of a GitHub release; install.sh itself is
## served from the repository (raw.githubusercontent.com). To publish somewhere else, bake another
## download location into dist/install.sh with
##   make package BASE_URL=https://example.com/tiup-backup
BASE_URL ?=

package:
	@mkdir -p $(DIST)
	@if [ -n "$(BASE_URL)" ]; then \
		sed 's|^DEFAULT_BASE_URL=.*|DEFAULT_BASE_URL="$(BASE_URL)"|' scripts/install.sh > $(DIST)/install.sh; \
	else cp scripts/install.sh $(DIST)/install.sh; fi
	@chmod 755 $(DIST)/install.sh
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		stage=$(DIST)/stage-$$os-$$arch; \
		rm -rf $$stage && mkdir -p $$stage; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="-s -w" -o $$stage/$(BIN) $(PKG) || exit 1; \
		cp $(DIST)/install.sh $$stage/install.sh; \
		tar -C $$stage -czf $(DIST)/$(BIN)-$$os-$$arch.tar.gz $(BIN) install.sh || exit 1; \
		(cd $(DIST) && sha256sum $(BIN)-$$os-$$arch.tar.gz > $(BIN)-$$os-$$arch.tar.gz.sha256) || exit 1; \
		rm -rf $$stage; \
		echo "built $(DIST)/$(BIN)-$$os-$$arch.tar.gz"; \
	done

clean:
	rm -rf $(DIST)
