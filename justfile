set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

default:
    @just --list

# -----------------------------------------------------------------------------
# Development
# -----------------------------------------------------------------------------

fmt:
    go fmt ./...

test:
    go test ./...

test-verbose:
    go test -v ./...

test-race:
    go test -race ./...

coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

coverage-html:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out

bench:
    go test -bench=. -benchmem ./...

vet:
    go vet ./...

lint:
    golangci-lint run

tidy:
    go mod tidy

check:
    just fmt
    just test
    just vet
    just lint

# -----------------------------------------------------------------------------
# Running
# -----------------------------------------------------------------------------

run *args:
    go run ./cmd/wrk {{args}}

link *args:
    go run ./cmd/wrk link {{args}}

new destination:
    go run ./cmd/wrk new {{destination}}

# -----------------------------------------------------------------------------
# Building
# -----------------------------------------------------------------------------

build:
    mkdir -p bin
    go build -o bin/wrk ./cmd/wrk

install:
    go install ./cmd/wrk

release:
    mkdir -p bin
    CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags="-s -w" \
        -o bin/wrk \
        ./cmd/wrk

clean:
    rm -rf bin
    rm -f coverage.out

# -----------------------------------------------------------------------------
# Information
# -----------------------------------------------------------------------------

deps:
    go list -m all

outdated:
    go list -u -m all

env:
    go env

# -----------------------------------------------------------------------------
# Convenience
# -----------------------------------------------------------------------------

dev:
    just fmt
    just test

ci:
    just tidy
    just check
