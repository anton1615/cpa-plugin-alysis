GO ?= go
VERSION ?= dev
TARGET_OS ?= $(shell $(GO) env GOOS)
ARCH ?= $(shell $(GO) env GOARCH)
BUILD_DIR ?= build
DIST_DIR ?= dist

LIBRARY_EXT_linux = so
LIBRARY_EXT_darwin = dylib
LIBRARY_EXT_windows = dll
LIBRARY_NAME = alysis.$(LIBRARY_EXT_$(TARGET_OS))

.PHONY: build test fmt clean package

build:
	@case "$(TARGET_OS)" in linux|darwin|windows) ;; *) echo "Unsupported target OS: $(TARGET_OS)" >&2; exit 1 ;; esac
	mkdir -p "$(BUILD_DIR)"
	CGO_ENABLED=1 GOOS="$(TARGET_OS)" GOARCH="$(ARCH)" $(GO) build -trimpath -buildmode=c-shared -o "$(BUILD_DIR)/$(LIBRARY_NAME)" .

test:
	$(GO) test ./...

fmt:
	gofmt -w .

clean:
	rm -rf "$(BUILD_DIR)" "$(DIST_DIR)"

package: build
	bash scripts/package.sh "$(VERSION)" "$(ARCH)" "$(BUILD_DIR)/$(LIBRARY_NAME)" "$(DIST_DIR)" "$(TARGET_OS)"
