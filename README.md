# Zoomrs - Zoom meetings recordings download service

[![Go Report Card](https://goreportcard.com/badge/github.com/parMaster/zoomrs)](https://goreportcard.com/report/github.com/parMaster/zoomrs)
[![Go](https://github.com/parMaster/zoomrs/actions/workflows/go.yml/badge.svg)](https://github.com/parMaster/zoomrs/actions/workflows/go.yml)
[![License](https://img.shields.io/github/license/parMaster/zoomrs)](https://github.com/parMaster/zoomrs/blob/main/LICENSE)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/parMaster/zoomrs?filename=go.mod)

Save thousands of dollars on Zoom Cloud Recording Storage! Download records automatically and store locally. Provide simple but effective web frontend to watch and share meeting recordings.

## Features

- Download Zoom Cloud Recordings automatically
- Delete/Trash recordings from Zoom Cloud once every instance confirms its downloaded copy
- Specify which types of recordings to download (shared screen, gallery view, active speaker) and which to ignore (audio only, chat, etc.)
- Host a simple web frontend to watch and share recordings
- Run multiple instances of the service for redundancy

## Installation
Zoomrs can be installed as a systemd service or run from the console as a persistent process or a set of CLI tools. It can be run as a Docker container as well.

## Prerequisites
### Zoom API credentials
Zoom API credentials are required to download recordings. You can get them at https://marketplace.zoom.us/develop/create. You need to create JWT app and copy API key and secret to the configuration file.

Add the following scopes to the App:

- `/recording:master`
- `/recording:read:admin`
- `/recording:write:admin`
- `/report:read:admin`

### Google OAuth credentials *(only if you want to host web frontend)*
Google OAuth credentials are required to authenticate users. You can get them at https://console.cloud.google.com/apis/credentials. You need to create OAuth client ID and copy client ID and secret to the configuration file. Mind authorized redirect URIs - local domains are not allowed, so you need to use a public domain name or IP address.

### Google OAuth authorized users *(only if you want to host web frontend)*
You need to specify the list of users that are allowed to access the web frontend. Their email addresses should be specified in the configuration file.

## Configuration
See `config/config_example.yml` for example configuration file, available options and their descriptions. Copy it to `config/config.yml` and edit it to your needs.

## Running the service
- To run a binary distribution, please refer to the [README](https://github.com/parMaster/zoomrs/dist/README.md) in `dist` directory.

- To build from source, proceed with this manual.

### Foreground mode
> [!NOTE]
> this is not recommended for production use, use systemd service instead or run it in a Docker container

1. Clone the repository from GitHub

	```sh
	git clone https://github.com/parMaster/zoomrs.git
	```

2. Make sure `config/config.yml` exists and is configured properly
3. Run `make run` to build the binary and run it in foreground mode

	```sh
	make run
	```
4. To stop the service press `Ctrl+C` (or send `SIGINT`, `SIGTERM` signal to the process)

### Systemd service
1. Clone the repository and put the configuration at `/etc/zoomrs/config.yml` (deploy never copies it from the repo)
2. Run `make deploy` from the `deploy/` folder to build the binary and copy everything where it belongs (see `deploy/Makefile` for details), enable and run the service
	```sh
	cd deploy && make deploy
	```
3. Run `make status` to check the status of the service

	```sh
	make status
	```

Log files are located at `/var/log/zoomrs.log` and `/var/log/zoomrs.err` by default. The unit is sandboxed and can only write to `/data`, see [deploy/README.md](deploy/README.md).

### Docker container
1. Clone the repository from GitHub

	```sh
	git clone https://github.com/parMaster/zoomrs.git
	```

2. Make sure `config/config.yml` exists and is configured properly
3. Check configuration parameters in Dockerfile and docker-compose.yml
4. Build and run container

	```sh
	docker compose up -d
	```

## Usage
### Web frontend
Web frontend is available at `http://localhost:8099` by default. You can change the port in the configuration file (`server.listen` parameter).

### Web frontend Pages

```http
GET `/`
```
Displays the list of recordings. Each recording has a link to share (view) it. Recordings are sorted by date in descending order. Login is required to view the list. Google OAuth is used for authentication. Access is restricted to users with email addresses from the list specified in the configuration file (see `server.managers`).

Share button is available for each recording, it generates a link to view the recording. Share link looks like:

```http
GET `/watch/834d0992ad0d632cf6c3174b975cb5e5?uuid=kzbiTyvQQp2fW6biu8Vy%2BQ%3D%3D`
```
Displays the page with the meeting title and player to watch the recording. Simple controls besides the embeded player is providing are available.

## API

#### GET `/status`
Returns the status of the service and Zoom cloud storage usage stats. If the service is running, returns `200 OK` and the following JSON. Example response:
```json
{
  "cloud": {
    "date": "2023-07-09",
    "free_usage": "495 GB",
    "plan_usage": "0",
    "usage": "27.98 GB",
    "usage_percent": 5
  },
  "stats": {
    "downloaded": {
      "count": 6529,
      "size_gb": 2148,
      "size_mb": 2200412
    }
  },
  "status": "OK",
  "storage": {
    "free": "1.2 TB",
    "total": "3.6 TB",
    "usage_percent": 63,
    "used": "2.2 TB"
  }
}
```
status can be:
- `OK` when everything is downloaded and nothing has failed
- `LOADING` when there are `queued` or `downloading` recordings present
- `FAILED` when there are only `downloaded` and `failed` recordings in the database

`stats` section contains number of recordings and their total size in GB and MB grouped by status

`last_downloaded` is the start time of the latest meeting that has a downloaded video. It is left out until the first video is downloaded (a fresh install, or only audio so far) - the rest of the response is still returned.

`cloud` section contains Zoom cloud storage usage stats. `date` is the last time the stats were updated (it is updated every 24 hours, so if you see the date is not today, it means the stats dodn't change since then), `free_usage` is the amount of free storage, `plan_usage` is the amount of storage available for the current plan, `usage` is the amount of storage used by recordings, `usage_percent` is the percentage of used storage.

`storage` section contains the stats of the local storage. `free` is the amount of free storage, `total` is the total amount of storage, `usage_percent` is the percentage of used storage, `used` is the amount of used storage.

This API is useful for monitoring the service status and triggering alerts when something goes wrong.

Another example response, when there are recordings in `queued` and `downloading` status (only relevant fields are shown):
```json 
{
  "stats": {
    "downloaded": {
      "count": 5292,
      "size_gb": 1765,
      "size_mb": 1808161
    },
    "downloading": {
      "count": 1,
      "size_gb": 0,
      "size_mb": 666
    },
    "queued": {
      "count": 88,
      "size_gb": 27,
      "size_mb": 28044
    }
  },
  "status": "LOADING"
}
```

#### GET `/check`
Auth required. Runs a consistency check of the repository (see `check` cli tool cmd, it's the same). Example response:
```json
{
  "checked": 5278,
  "error": null
}
```

#### GET `/stats[/<K|M|G>]`
Auth required. Returns the total size of the recordings grouped by date. Optional parameter `K`, `M` or `G` can be used to specify the size in KB, MB or GB respectively. If no parameter is specified, the size is returned in bytes. When nothing is downloaded yet, the response is an empty object `{}`. Example response:
```json
{
	"2023-03-20":31,
	"2023-03-21":13,
	"2023-03-22":36,
	"2023-03-23":19,
	"2023-03-24":41
}
```

#### GET `/meetingsLoaded/{accessKey}`
`accessKey` is checked against server.access_key_salt config option. This api is called to ask which meetings from the list are loaded, list is passed as a JSON array of UUIDs in the request body.
Request example:
```json
{
	"meetings":{
		"in7MDVrTS5adXWFwsCwoYg==",
		"0ao3hvbxQvqU2wkpXjbwhw==",
		"pEbVqZ5jQP6+NY0ewvZ+wg==",
		"uOoMA3wcSF65PtwTDw/k1w=="
	}
}
``` 

Response when all meetings are loaded:
```json
{
	"result":"ok",
	"loaded":["in7MDVrTS5adXWFwsCwoYg==", "0ao3hvbxQvqU2wkpXjbwhw==", "pEbVqZ5jQP6+NY0ewvZ+wg==", "uOoMA3wcSF65PtwTDw/k1w=="]
}
```
Response when some meetings are not loaded:
```json
{
	"result":"pending",
	"loaded":["in7MDVrTS5adXWFwsCwoYg==", "pEbVqZ5jQP6+NY0ewvZ+wg=="]
}
```
`loaded` lists every meeting of the request this instance has a full copy of; every meeting is checked, a pending one does not end the check. `result` is `ok` only when that is all of them. `loaded` is always there (`[]` when nothing is loaded): an instance that is not updated yet answers with `result` alone, and the `trash` command reads such an answer as "all loaded" on `ok` and "none loaded" on `pending`.

A meeting counts as loaded only when it has records, every record is `downloaded` and its file is on disk with the expected size. A file that is missing or can't be read keeps the meeting out of `loaded`, so the caller never deletes a recording from Zoom that this instance can't show a copy of. A meeting with no records here (too short, or nothing of the syncable types, and kept in Zoom Cloud because `client.delete_skipped` is off) is never loaded, so `trash` leaves it in Zoom Cloud.

## CLI tool
Zoomrs comes with a CLI tool to trash/delete recordings from Zoom Cloud. It is useful when running miltiple servers and you want to delete recordings from Zoom Cloud only after all servers have downloaded them. CLI tool is located at `cmd/cli/main.go`. Run `make` to build it and put to `dist/zoomrs-cli`.
It can be run like this:

```sh
go run ./cmd/cli --cmd check
```

or like this:

```sh
./dist/zoomrs-cli --cmd check
```

Available commands:
- `check` - checks the consistency of the repository: if all recordings are downloaded and if all downloaded recordings are present on the disk, also the size of each recording file is checked. Run this command periodically to make sure everything is OK. 
Run it like this:

```sh
./dist/zoomrs-cli --cmd check
```
	Example output:
	```
	2023/06/19 17:15:01 [INFO]  starting CheckConsistency
	2023/06/19 17:15:01 [INFO]  Checked files: 5278
	2023/06/19 17:15:01 [INFO]  CheckConsistency: OK, 5278
	```
- `trash` - trashes recordings from Zoom Cloud. Run it like this:

```sh
./zoomrs-cli --dbg --cmd trash
```

	With no `--days` it covers the last 30 days in one listing call, so a run that was missed (a power cut, a server that was down) is caught up by the next one. It asks every instance from `commander.instances` which of the listed meetings it has loaded (see `/meetingsLoaded` above) and trashes exactly the meetings that **all** of them confirm. A meeting that is still downloading somewhere, or that one instance lacks, stays in Zoom Cloud and does not hold back the others. Nothing is trashed when no instances are configured. Meetings are moved to trash, or deleted permanently if `client.delete_downloaded` is true.

	An instance that does not answer is asked again a minute later, up to 10 times; instances that did answer are not asked again. If it still gives no answer, nothing is trashed in that run and the next run tries again.

	`--days N` limits the run to one day, `N` days before today (`0` is today), as before. `--trash N` still works as a deprecated alias of `--days N`; giving both with different values is an error.

> [!NOTE]
> There is no grace day with the default window: yesterday's (and today's) meetings are trashed as soon as every instance confirms them. Use `--days 2` to keep the old behavior of trashing only the day before yesterday.

	`--force` trashes everything in range without asking any instance. It needs an explicit `--days` (or `--trash`), so it can never reach the whole 30-day window.

	Cron job line example:
```sh
00 10 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --cmd trash --config ../config/config_cli.yml >> /var/log/cron.log 2>&1
```

	will trash the confirmed recordings of the last 30 days every day at 10:00 AM. `--config` option is used to specify the path to the configuration file. `--dbg` option can be used to enable debug logging. Logs are written to stdout, and redirected to `/var/log/cron.log` in the example above.

- `cloudcap` - trims recordings from Zoom Cloud to avoid exceeding the storage limit. Leaves `Client.CloudCapacityHardLimit` bytes of the most recent recordings (review the value in config before running!), trashes the rest. Cron job line to run it every day at 5:30 AM (don't mind the paths, they are specific to my setup, use your own):
```sh
30 05 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --dbg --cmd cloudcap --config ../config/config_cli.yml >> /var/log/cron.log 2>&1
```
- `sync` - syncs recordings from Zoom Cloud. Run it like this:
```sh
./zoomrs-cli --dbg --cmd sync
```

	With no `--days` it lists the last 30 days in one listing call, saves the meetings it does not know yet and downloads their records, so a missed run is caught up by the next one. `--days N` limits the run to one day, `N` days before today (`--days 1` is yesterday, `--days 0` is today).

	The download is limited to the meetings Zoom lists for the run: queued, failed and unfinished records of other meetings are left as they are. Of the listed meetings:
	- records stuck in `downloading` (a run died mid-download) are put back in the queue, whatever their age
	- `failed` records are put back in the queue only if the recording started within the last 3 days; older ones stay `failed`
	- a record is put back once per run, so one that fails every time does not keep the run going. The run stops when the queue is empty or after 12 hours.

	However the run ends (finished, 12 hours passed, an error, a signal), it logs the records of the listed meetings that are left `failed` - meeting topic, record id and start time. Nothing is logged when there are none.

	Downloading never trashes or deletes anything in Zoom Cloud, only the `trash` command does.

	Cron job line example:
```sh
00 03 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --cmd sync --config ../config/config_cli.yml >> /var/log/zoomrs.cron.log 2>&1
```

will sync the recordings of the last 30 days every day at 3:00 AM. `--config` option is used to specify the path to the configuration file. `--dbg` option can be used to enable debug logging. Logs are written to stdout, and redirected to `/var/log/zoomrs.cron.log` in the example above.

### One run at a time
`sync`, `trash` and `cloudcap` take a lock before they do anything: a `flock` on a file next to the database, named after it (`storage.path: file:/data/_db/main.db?mode=rwc` gives `/data/_db/main.db.lock`). If another run holds it, the new one logs that (with the PID of the holder) and exits without calling Zoom. The lock is gone when the process ends, also when it is killed, so there is nothing to clean up; the file itself stays. `check` and the UI take no lock. The service does not take it either: turn `server.sync_job` and `server.download_job` off on a server where the CLI does the syncing. On Windows there is no lock.

### Upgrading: `client.trash_downloaded` is gone
The download step used to trash (`client.trash_downloaded`) or delete (`client.delete_downloaded`) a meeting in Zoom Cloud as soon as its last record was downloaded. It no longer does, in the service and in the CLI: a meeting is removed from Zoom Cloud only by the `trash` command, after every instance confirms it. `trash_downloaded` is ignored, and a config that still sets it logs a warning on load. `delete_downloaded` now only chooses between trash (false) and permanent deletion (true) for `trash` and for `delete_skipped`. If you relied on `trash_downloaded`, schedule the `trash` command.


> [!NOTE] 
> CLI tool uses different configuration file then the server with different Zoom API credentials to avoid spoiling services's auth token when running CLI. Also, running multi-server setup you want to sync recordings only after all servers have downloaded them, so you need to run CLI tool on one of the servers, allow syncing records in CLI config and deny it in servers configs.

## Running multiple instances
You can run multiple instances of the service to increase reliability, duplicate downloaded data for redundancy. Each instance should have its own configuration file and its own database file. Each instance should have its own Zoom API credentials. Consider following setup as an example:
1. One main instance that downloads recordings and hosts web frontend (see `config/config_example.yml` for example configuration file). Enable sync and download for this instance: `server.sync_job: true` and `server.download_job: true` in the configuration file, set oauth credentials and authorized users.
2. One or many secondary instances that download recordings but don't host web frontend. Two options are available here:
	- Run the service with `server.sync_job: true` and `server.download_job: true` in the configuration file. This way download job will run somewhere from 00:00 to 01:00 am.
	- Run the service with `server.sync_job: false` and `server.download_job: false` so it will just host the API. Run downloader with cron job (see `sync` cmd crontab line example in the previous section). This way you can set the time to run the download job
3. Run cleanup job on one of the instances (see `trash` cmd crontab line example in the previous section). Use configuration file that enumerates all the instances in `server.instances` section. This way cleanup job will ask all the instances and trash/delete from Zoom Cloud only the meetings that all the instances have downloaded.

> [!NOTE]
> Copy yesterday's recordings from "Main" instance to "Secondary" instance
> Secondary instance can run something like this to copy yesterday's recordings from "Main" instance:

```sh
sleep 1s && date && scp -r server.local:/data/`date --date="yesterday" +%Y-%m-%d` /data/ && date
```

> [!NOTE]
> Database backup
> Backup database file regularly to prevent data loss. See example shell script at `deploy/backup_db.sh`. It can be run as a cron job like this:

```sh
0 10 * * * sh $HOME/go/src/zoomrs/deploy/backup_db.sh
```

## Contributing
Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change. Check the existing issues to see if your problem is already being discussed or if you're willing to help with one of them. Tests are highly appreciated.

`make test` runs offline against fake Zoom servers and temp directories; `make lint` runs the pinned golangci-lint. Tests against the real Zoom API are behind a build tag and read credentials from `config/config_cli.yml`: `go test -tags integration ./client`. Careful: they call the real delete endpoint.

## License
[GNU GPLv3](https://choosealicense.com/licenses/gpl-3.0/) © [Dmytro Borshchanenko](https://github.com/parMaster) 2023

## Responsible disclosure
If you have any security issue to report, contact project owner directly at [master@parMaster.com.ua](mailto:master@parMaster.com.ua) or use Issues section of this repository.

## Responsibility
The author of this project is not responsible for any damage caused by the use of this software. Use it at your own risk. However, the software is being used in production at least since May 2023 on a number of devices, processing hundreds of GB of data every day and is considered stable.

## Credits
- [lgr](github.com/go-pkgz/lgr) - simple but effective logging package
- [go-sqlite3](github.com/mattn/go-sqlite3) as a database driver
- [go-pkgz/auth](github.com/go-pkgz/auth) - powerful authentication middleware
