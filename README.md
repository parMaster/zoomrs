# Zoomrs - Zoom meetings recordings download service

[![Go Report Card](https://goreportcard.com/badge/github.com/parMaster/zoomrs)](https://goreportcard.com/report/github.com/parMaster/zoomrs)
[![Go](https://github.com/parMaster/zoomrs/actions/workflows/go.yml/badge.svg)](https://github.com/parMaster/zoomrs/actions/workflows/go.yml)
[![License](https://img.shields.io/github/license/parMaster/zoomrs)](https://github.com/parMaster/zoomrs/blob/main/LICENSE)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/parMaster/zoomrs?filename=go.mod)

Save thousands of dollars on Zoom Cloud Recording Storage! Download records automatically and store locally. Provide simple but effective web frontend to watch and share meeting recordings.

## Features

- Download Zoom Cloud Recordings automatically
- Delete/Trash downloaded recordings from Zoom Cloud after download
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
1. Repeat steps 1 and 2 from the previous section
2. Run `make deploy` to build the binary and copy everything where it belongs (see `Makefile` for details), enable and run the service
	```sh
	make deploy
	```
3. Run `make status` to check the status of the service

	```sh
	make status
	```

Log files are located at `/var/log/zoomrs.log` and `/var/log/zoomrs.err` by default.

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
Auth required. Returns the total size of the recordings grouped by date. Optional parameter `K`, `M` or `G` can be used to specify the size in KB, MB or GB respectively. If no parameter is specified, the size is returned in bytes. Example response:
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
`accessKey` is checked against server.access_key_salt config option. This api is called to ask if every meeting from the list is loaded, list is passed as a JSON array of UUIDs in the request body.
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
	"result":"ok"
}
```
Response when some meetings are not loaded:
```json
{
	"result":"pending"
}
```

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
./zoomrs-cli --dbg --cmd trash --trash 2
```

	where `2` is 2 days before today, so all the recordings from the bay before yesterday will be trashed. This is designed this way to run it as a cron job every day. Cron job line example:
```sh
00 10 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --cmd trash --trash 2 --config ../config/config_cli.yml >> /var/log/cron.log 2>&1
```

	will trash all recordings from the day before yesterday every day at 10:00 AM. `--config` option is used to specify the path to the configuration file. `--dbg` option can be used to enable debug logging. Logs are written to stdout, and redirected to `/var/log/cron.log` in the example above.

- `cloudcap` - trims recordings from Zoom Cloud to avoid exceeding the storage limit. Leaves `Client.CloudCapacityHardLimit` bytes of the most recent recordings (review the value in config before running!), trashes the rest. Cron job line to run it every day at 5:30 AM (don't mind the paths, they are specific to my setup, use your own):
```sh
30 05 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --dbg --cmd cloudcap --config ../config/config_cli.yml >> /var/log/cron.log 2>&1
```
- `sync` - syncs recordings from Zoom Cloud. Run it like this:
```sh
./zoomrs-cli --dbg --cmd sync --days 1
```

	`--days` parameter used with the value of `1` to sync all the yesterday recordings (1 day before today). This is designed this way to run it as a cron job. Cron job line example:
```sh
00 03 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --cmd sync --days 1 --config ../config/config_cli.yml >> /var/log/zoomrs.cron.log 2>&1
```

will sync all recordings from the yesterday every day at 3:00 AM. `--config` option is used to specify the path to the configuration file. `--dbg` option can be used to enable debug logging. Logs are written to stdout, and redirected to `/var/log/cron.log` in the example above.


> [!NOTE] 
> CLI tool uses different configuration file then the server with different Zoom API credentials to avoid spoiling services's auth token when running CLI. Also, running multi-server setup you want to sync recordings only after all servers have downloaded them, so you need to run CLI tool on one of the servers, allow syncing records in CLI config and deny it in servers configs.

## Running multiple instances
You can run multiple instances of the service to increase reliability, duplicate downloaded data for redundancy. Each instance should have its own configuration file and its own database file. Each instance should have its own Zoom API credentials. Consider following setup as an example:
1. One main instance that downloads recordings and hosts web frontend (see `config/config_example.yml` for example configuration file). Enable sync and download for this instance: `server.sync_job: true` and `server.download_job: true` in the configuration file, set oauth credentials and authorized users.
2. One or many secondary instances that download recordings but don't host web frontend. Two options are available here:
	- Run the service with `server.sync_job: true` and `server.download_job: true` in the configuration file. This way download job will run somewhere from 00:00 to 01:00 am.
	- Run the service with `server.sync_job: false` and `server.download_job: false` so it will just host the API. Run downloader with cron job (see `sync` cmd crontab line example in the previous section). This way you can set the time to run the download job
3. Run cleanup job on one of the instances (see `trash` cmd crontab line example in the previous section). Use configuration file that enumerates all the instances in `server.instances` section. This way cleanup job will check all the instances for consistency and trash/delete recordings from Zoom Cloud only if all the instances have downloaded them. Disable deleting and trashing downloaded recordings (`client.trash_downloaded: false` and `client.delete_downloaded: false` in the configuration file) on every other instance but this one.

> [!NOTE]
> Copy yesterday's recordings from "Main" instance to "Secondary" instance
> Secondary instance can run something like this to copy yesterday's recordings from "Main" instance:

```sh
sleep 1s && date && scp -r server.local:/data/`date --date="yesterday" +%Y-%m-%d` /data/ && date
```

> [!NOTE]
> Database backup
> Backup database file regularly to prevent data loss. See example shell script at `dist/backup_db.sh`. It can be run as a cron job like this:

```sh
0 10 * * * sh $HOME/go/src/zoomrs/backup_db.sh
```

## Contributing
Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change. Check the existing issues to see if your problem is already being discussed or if you're willing to help with one of them. Tests are highly appreciated.

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
