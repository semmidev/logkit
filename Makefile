.PHONY: help build test bench vet fmt run-basic run-http

# Default target listing all commands
help:
	@echo "Available commands:"
	@echo "  make build      - Build all packages"
	@echo "  make test       - Run all unit tests"
	@echo "  make bench      - Run performance benchmarks"
	@echo "  make vet        - Run go vet checks"
	@echo "  make fmt        - Format Go source code"
	@echo "  make run-basic  - Run the basic application example"
	@echo "  make run-http   - Run the HTTP application server example"

build:
	go build ./...

test:
	go test -v ./...

bench:
	go test -bench=. -benchmem ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

run-basic:
	go run ./examples/basicapp

run-http:
	go run ./examples/httpapp
