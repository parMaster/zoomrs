build:
	go build -o dist/zoomrs -v

test:
	go test ./...

.PHONY: build test

.DEFAULT_GOAL : build