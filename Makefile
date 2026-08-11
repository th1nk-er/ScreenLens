APP      := screenlens
CMD      := ./cmd/screenlens
BIN_DIR  := bin
CONFIG   ?= config.yaml
GO       ?= go
GOFLAGS  ?=
LDFLAGS  ?=
GUI_TAGS    ?= gui

ifeq ($(OS),Windows_NT)
  GUI_LDFLAGS ?= -H=windowsgui
else
  GUI_LDFLAGS ?=
endif

ifeq ($(OS),Windows_NT)
  GUI_BINARY     := $(BIN_DIR)/$(APP).exe
  CONSOLE_BINARY := $(BIN_DIR)/$(APP)-console.exe
else
  GUI_BINARY     := $(BIN_DIR)/$(APP)
  CONSOLE_BINARY := $(BIN_DIR)/$(APP)-console
endif

.PHONY: all build build-gui build-console fmt tidy test race vet check run run-gui install install-gui clean

ifeq ($(OS),Windows_NT)
build: build-gui
else
build: build-console
endif

all: check build

build-gui:
	@$(GO) build $(GOFLAGS) -tags "$(GUI_TAGS)" -ldflags "$(GUI_LDFLAGS) $(LDFLAGS)" -o "$(GUI_BINARY)" $(CMD)

build-console:
	@$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(CONSOLE_BINARY)" $(CMD)

fmt:
	@gofmt -w cmd internal

tidy:
	@$(GO) mod tidy

test:
	@$(GO) test ./...

race:
	@$(GO) test -race ./...

vet:
	@$(GO) vet ./...

check: fmt tidy test vet

run:
	@$(GO) run $(CMD) -config "$(CONFIG)"

run-gui:
	@$(GO) run -tags "$(GUI_TAGS)" -ldflags "$(GUI_LDFLAGS)" $(CMD) -config "$(CONFIG)"

install:
	@$(GO) install $(CMD)

install-gui:
	@$(GO) install -tags "$(GUI_TAGS)" -ldflags "$(GUI_LDFLAGS)" $(CMD)

clean:
	@$(GO) clean
ifeq ($(OS),Windows_NT)
	@if exist "$(BIN_DIR)" rmdir /s /q "$(BIN_DIR)"
else
	@rm -rf "$(BIN_DIR)"
endif
