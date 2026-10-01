# Deployment helpers for zoomrs. Run these ON THE VPS, from this
# directory (deploy/, inside the cloned repo) — `deploy` builds the
# parent repo itself via `cd .. && $(MAKE) buildsvc`, so a `git pull` in
# the repo root before running this is what actually picks up new code.
#
# config.yml is NOT handled here on purpose: it must already exist at
# /etc/zoomrs/config.yml before the first `make deploy` (systemd just
# retries via Restart=always if it's missing). Never create config.yml
# inside this repo clone — it holds secrets and risks getting committed.

USER=$(shell whoami)
GROUP=$(shell id -gn)

status:
	sudo systemctl status zoomrs.service

stop:
	sudo systemctl stop zoomrs.service

start:
	sudo systemctl start zoomrs.service

deploy:
	cd .. && $(MAKE) buildsvc
	sudo systemctl stop zoomrs.service || true
	sudo cp ../dist/zoomrs /usr/bin/
	sudo chown $(USER):$(GROUP) /usr/bin/zoomrs
	sudo mkdir -p /etc/zoomrs
	sudo chown $(USER):$(GROUP) /etc/zoomrs
	sed "s/%USER%/$(USER)/g" zoomrs.service | sudo tee /etc/systemd/system/zoomrs.service > /dev/null
	sudo systemctl daemon-reload
	sudo systemctl enable zoomrs.service
	sudo systemctl start zoomrs.service

.PHONY: status stop start deploy
