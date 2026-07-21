.PHONY: build test lint

build:
	go build -o bin/intuizi .

test:
	go test ./...

lint:
	golangci-lint run
