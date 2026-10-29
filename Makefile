# Headless server: pure Go, so it cross-compiles.
#   make server
#   make server GOOS=linux GOARCH=arm64
#   make servers
# Desktop window: links the host webview, so it builds only for this machine.
#   make desktop

HOST_OS := $(shell go env GOOS)
HOST_ARCH := $(shell go env GOARCH)
GOOS ?= $(HOST_OS)
GOARCH ?= $(HOST_ARCH)

ifeq ($(GOOS),windows)
  TARGET_EXE := .exe
else
  TARGET_EXE :=
endif

ifeq ($(HOST_OS),windows)
  HOST_EXE := .exe
  DESKTOP_LDFLAGS := -H windowsgui -s -w
else
  HOST_EXE :=
  DESKTOP_LDFLAGS := -s -w
endif

ifneq ($(GOOS)/$(GOARCH),$(HOST_OS)/$(HOST_ARCH))
  SERVER_OUT := bin/hearth-$(GOOS)-$(GOARCH)$(TARGET_EXE)
else
  SERVER_OUT := bin/hearth$(TARGET_EXE)
endif

.PHONY: server servers desktop test

server: export CGO_ENABLED=0
server: export GOOS:=$(GOOS)
server: export GOARCH:=$(GOARCH)
server:
	go build -trimpath -ldflags="-s -w" -o $(SERVER_OUT) ./cmd/hearth

servers:
	$(MAKE) server GOOS=darwin GOARCH=amd64
	$(MAKE) server GOOS=darwin GOARCH=arm64
	$(MAKE) server GOOS=linux GOARCH=amd64
	$(MAKE) server GOOS=linux GOARCH=arm64
	$(MAKE) server GOOS=windows GOARCH=amd64
	$(MAKE) server GOOS=windows GOARCH=arm64

desktop: export CGO_ENABLED=1
desktop:
ifneq ($(GOOS)/$(GOARCH),$(HOST_OS)/$(HOST_ARCH))
	$(error the desktop window links this machine's webview and cannot be cross-compiled; build it on $(GOOS)/$(GOARCH))
endif
	go build -tags desktop -trimpath -ldflags="$(DESKTOP_LDFLAGS)" -o bin/hearth-desktop$(HOST_EXE) ./cmd/hearth

test:
	go test ./...
