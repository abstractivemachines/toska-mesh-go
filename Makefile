MODULE   := github.com/toska-mesh/toska-mesh-go
PROTO_DIR := ../proto
PB_DIR   := pkg/meshpb

export PATH := $(HOME)/go/bin:$(PATH)

.PHONY: generate build test lint clean

generate:
	@mkdir -p $(PB_DIR)
	protoc \
		--go_out=$(PB_DIR) \
		--go_opt=Mdiscovery.proto=$(MODULE)/$(PB_DIR) \
		--go_opt=module=$(MODULE)/$(PB_DIR) \
		--go-grpc_out=$(PB_DIR) \
		--go-grpc_opt=Mdiscovery.proto=$(MODULE)/$(PB_DIR) \
		--go-grpc_opt=module=$(MODULE)/$(PB_DIR) \
		--proto_path=$(PROTO_DIR) \
		$(PROTO_DIR)/discovery.proto

build: build-examples

build-examples:
	go build -o bin/hello-mesh-service ./examples/hello-mesh-service

test:
	go test -race ./...

lint:
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

clean:
	rm -rf bin/
