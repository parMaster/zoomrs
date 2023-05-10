build:
	go build -o dist/zoomrs -v

test:
	go test ./...

run:
	go run ./ --dbg --config ./config/config_dbg.yml

.PHONY: build test

.DEFAULT_GOAL : build