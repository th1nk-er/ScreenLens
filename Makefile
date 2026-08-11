APP      := screenlens
CMD      := ./cmd/screenlens
BIN_DIR  := bin
CONFIG   ?= config.yaml
GO       ?= go
GOFLAGS  ?=
LDFLAGS  ?=

ifeq ($(OS),Windows_NT)
  BINARY := $(BIN_DIR)/$(APP).exe
else
  BINARY := $(BIN_DIR)/$(APP)
endif

.PHONY: all build fmt tidy test race vet check run install clean

all: check build

build:
	@$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o "$(BINARY)" $(CMD)

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

install:
	@$(GO) install $(CMD)

clean:
	@$(GO) clean
ifeq ($(OS),Windows_NT)
	@if exist "$(BIN_DIR)" rmdir /s /q "$(BIN_DIR)"
else
	@rm -rf "$(BIN_DIR)"
endif
