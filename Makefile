.PHONY: build test lint
build:
	go build -o moira ./cmd/moira
test:
	go test ./...
lint:
	go vet ./... && test -z "$$(gofmt -l .)"
