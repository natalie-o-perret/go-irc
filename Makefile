.PHONY: build test vet lint clean ircd ircb ircx

# Build all binaries
build:
	go build ./...

# Build individual binaries to ./bin/
ircd:
	go build -o bin/ircd ./cmd/ircd

ircb:
	go build -o bin/ircb ./cmd/ircb

ircx:
	go build -o bin/ircx ./cmd/ircx

bins: ircd ircb ircx

# Run tests
test:
	go test ./... -race

# Short tests (skip slow integration tests)
test-short:
	go test ./... -short

# Verbose test output
test-v:
	go test ./... -v -race

# Static analysis
vet:
	go vet ./...

# Run all checks
check: vet test

clean:
	rm -rf bin/
	go clean ./...

