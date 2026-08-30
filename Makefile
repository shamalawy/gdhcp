MODULE      := github.com/shamalawy/gdhcp
BINARY      := dist/gdhcp
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X $(MODULE)/server/internal/version.Version=$(VERSION) \
           -X $(MODULE)/server/internal/version.Commit=$(COMMIT) \
           -X $(MODULE)/server/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all build web go-build test vet clean run-dev

all: build

## web: build the Angular frontend into web/dist/ (embedded by web/embed.go).
web:
	cd web && npm install && npm run build

## go-build: compile the gdhcp binary. Run `make web` first for a real UI;
## without it, web/dist/index.html's committed placeholder is embedded.
go-build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./server/cmd/gdhcp

## build: web + go-build, the full release build.
build: web go-build

## test: run all Go tests.
test:
	go test ./...

## vet: run go vet across the module.
vet:
	go vet ./...

## clean: remove build output.
clean:
	rm -rf dist web/dist-tmp

## run-dev: run the server directly from source against config.yaml (falls
## back to config.yaml.example if config.yaml doesn't exist yet), without a
## full release build.
run-dev:
	go run ./server/cmd/gdhcp serve --config $(or $(wildcard config.yaml),config.yaml.example)
