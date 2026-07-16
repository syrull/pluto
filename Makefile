BINARY := pluto
VERSION ?= dev
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build run test race vet fmt fmt-check tidy clean

all: fmt-check vet test build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

run:
	go run .

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

tidy:
	go mod tidy

clean:
	rm -rf bin
