GOBIN := $(shell go env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

.PHONY: all generate build test lint tools run tidy clean

all: generate build test

## generate: regenerate Go from proto/ (requires `make tools`)
generate:
	buf lint
	buf generate

## build: compile the control-plane binary into ./bin
build:
	mkdir -p bin
	go build -o bin/ogcode-control-plane ./cmd/ogcode-control-plane

## test: run the full suite with the race detector
test:
	go test -race ./...

## lint: vet + buf lint
lint:
	go vet ./...
	buf lint

## tools: install the protobuf/ConnectRPC codegen toolchain
tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
	go install github.com/bufbuild/buf/cmd/buf@latest

## run: build and run against ./control-plane.json
run: build
	./bin/ogcode-control-plane serve --config control-plane.json

tidy:
	go mod tidy

clean:
	rm -rf bin
