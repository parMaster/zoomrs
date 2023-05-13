build:
	go build 

test:
	go test ./...

run:
	go run ./ --dbg --config ./config/config_dbg.yml

.PHONY: build test run

.DEFAULT_GOAL : build