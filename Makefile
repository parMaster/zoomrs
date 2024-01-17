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

stop:
	sudo systemctl stop zoomrs.service

start:
	sudo systemctl start zoomrs.service

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


.PHONY: build buildsvc dbg test run info status deploy start stop cli release
