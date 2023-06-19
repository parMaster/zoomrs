B=$(shell git rev-parse --abbrev-ref HEAD)
BRANCH=$(subst /,-,$(B))
GITREV=$(shell git describe --abbrev=7 --always --tags)
REV=$(GITREV)-$(BRANCH)-$(shell date +%Y%m%d)

# get current user name
USER=$(shell whoami)
# get current user group
GROUP=$(shell id -gn)

.DEFAULT_GOAL: build

build:
	make buildsvc
	make buildcli

buildsvc:
	go build -o dist/zoomrs -v ./cmd/service

buildcli:
	go build -o dist/zoomrs-cli -v ./cmd/cli

info:
	- @echo "revision $(REV)"

test:
	go test ./...

run:
	go run ./cmd/service --config ./config/config.yml

dbg:
	go run ./cmd/service --dbg --config ./config/config_dbg.yml

status:
	sudo systemctl status zoomrs.service

deploy:
	make build
	sudo systemctl stop zoomrs.service || true
	sudo cp dist/zoomrs /usr/bin/
	sudo chown $(USER):$(GROUP) /usr/bin/zoomrs
	sed -i "s/%USER%/$(USER)/g" dist/zoomrs.service
	sudo cp dist/zoomrs.service /etc/systemd/system/
	sudo mkdir -p /etc/zoomrs
	sudo chown $(USER):$(GROUP) /etc/zoomrs
	cp config/config.yml /etc/zoomrs/
	sudo systemctl daemon-reload
	sudo systemctl enable zoomrs.service
	sudo systemctl start zoomrs.service

.PHONY: build buildsvc dbg test run info status deploy
