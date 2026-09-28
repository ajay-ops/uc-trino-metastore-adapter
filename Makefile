.PHONY: build test race vet fmt check generate check-generated

VERSION ?= dev
REVISION ?= unknown
BUILD_TIMESTAMP ?= unknown
BUILD_PACKAGE = github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability

build:
	CGO_ENABLED=0 go build -mod=readonly -buildvcs=false -trimpath -ldflags="-s -w -X $(BUILD_PACKAGE).Version=$(VERSION) -X $(BUILD_PACKAGE).Revision=$(REVISION) -X $(BUILD_PACKAGE).BuildTimestamp=$(BUILD_TIMESTAMP)" -o bin/uc-trino-metastore-adapter ./cmd/server

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal integration

check: vet test
	@test -z "$$(gofmt -l cmd internal integration)"

generate:
	python3 scripts/generate-thrift.py

check-generated:
	python3 scripts/generate-thrift.py --check
