B=$(shell git rev-parse --abbrev-ref HEAD)
BRANCH=$(subst /,-,$(B))
GITREV=$(shell git describe --abbrev=7 --always --tags)
REV=$(GITREV)-$(BRANCH)-$(shell date +%Y%m%d)

.DEFAULT_GOAL: build

build: info
	make buildsvc
	make buildcli

buildsvc:
	go build -o dist/zoomrs -v --ldflags="-X main.version=$(REV)" ./cmd/service

buildcli:
	go build -o dist/zoomrs-cli -v ./cmd/cli

info:
	- @echo "revision $(REV)"

test:
	go test ./...

# Built with the Go that go.mod selects, so the linter can always read the code.
# GOFLAGS is cleared because -mod=vendor breaks `go run pkg@version`.
GOLANGCI_LINT_VERSION=v2.14.0
lint:
	GOFLAGS= GOTOOLCHAIN=$$(go env GOVERSION) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

run: build
	go run ./cmd/service --config ./config/config.yml

dbg:
	go run ./cmd/service --dbg --config ./config/config_dbg.yml

cli:
	go build -o dist/zoomrs-cli -v ./cmd/cli
	./dist/zoomrs-cli --config ./config/config_cli.yml

# Prepare a release:
# git tag v1.2
# git push origin v1.2
# Make sure no secrets exposed in configs, .service file etc.
# Then run:
# make release
release:
	@echo release to dist/release
	mkdir -p dist/release
	cp config/config_example.yml dist/config.yml
#	take a look at the config we are going to show to the world
#	highlighting the values and cut off the comments in the console output
	@echo " \n\n +++ +++ +++ +++ +++ config.yml  +++ +++ +++ +++ +++  \n\n "
	cat dist/config.yml | sed 's/:/:\x1b[31m/g; s/#.*//' | awk '{print "\x1b[0m"$$0}'
	@echo " \n\n "
	cd dist && ./multibuild.sh $(GITREV)
	cd ..
	ls -l dist/release


.PHONY: build buildsvc buildcli dbg test lint run info cli release
