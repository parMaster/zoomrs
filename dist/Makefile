# This file is part of the zoomrs project: https://github.com/parmaster/zoomrs

# get current user name
USER=$(shell whoami)
# get current user group
GROUP=$(shell id -gn)

run:
	./zoomrs

status:
	sudo systemctl status zoomrs.service

stop:
	sudo systemctl stop zoomrs.service

start:
	sudo systemctl start zoomrs.service

deploy:
	sudo systemctl stop zoomrs.service || true
	sudo cp zoomrs /usr/bin/
	sudo chown $(USER):$(GROUP) /usr/bin/zoomrs
	sed -i "s/%USER%/$(USER)/g" zoomrs.service
	sudo cp zoomrs.service /etc/systemd/system/
	sudo mkdir -p /etc/zoomrs
	sudo chown $(USER):$(GROUP) /etc/zoomrs
	cp config.yml /etc/zoomrs/
	sudo systemctl daemon-reload
	sudo systemctl enable zoomrs.service
	sudo systemctl start zoomrs.service

cli:
	./zoomrs-cli --config ./config_cli.yml

.PHONY: build buildsvc dbg test run info status deploy start stop cli
