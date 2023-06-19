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
Zoomrs can be installed as a systemd service or run in foreground mode.

## Configuration
See `config/config_example.yaml` for example configuration file, available options and their descriptions. Copy it to `config/config.yaml` and edit it to your liking.

### Foreground mode 
1. Clone the repository from GitHub

		git clone https://github.com/parMaster/zoomrs.git

2. Make sure `config/config.yaml` exists and is configured properly
3. Run `make run` to build the binary and run it in foreground mode

		make run
4. To stop the service press `Ctrl+C`

### Systemd service
1. Repeat steps 1 and 2 from the previous section
2. Run `make deploy` to build the binary and copy everything where it belongs (see `Makefile` for details), enable and run the service

		make deploy
3. Run `make status` to check the status of the service

		make status

Log files are located at `/var/log/zoomrs.log` and `/var/log/zoomrs.err` by default.

## Usage
### Web frontend
Web frontend is available at `http://localhost:8099` by default. You can change the port in the configuration file (`server.listen` parameter).

### Web frontend Pages

#### GET `/`
Displays the list of recordings. Each recording has a link to share (view) it. Recordings are sorted by date in descending order. Login is required to view the list. Google OAuth is used for authentication. Access is restricted to users with email addresses from the list specified in the configuration file (see `server.managers`).

Share button is available for each recording, it generates a link to view the recording. Share link looks like:

#### GET `/watch/834d0992ad0d632cf6c3174b975cb5e5?uuid=kzbiTyvQQp2fW6biu8Vy%2BQ%3D%3D`
Displays the page with the meeting title and player to watch the recording. Simple controls besides the embeded player is providing are available.

### API

#### GET `/status`
Returns the status of the service. If the service is running, returns `200 OK` and the following JSON. Example response:
```json
{
	"stats":{
		"downloaded":{
			"count":5278,
			"size_gb":1762,
			"size_mb":1804660
		}
	},
	"status":"OK"
}
```
status can be:
- `OK` when everything is downloaded and nothing has failed
- `LOADING` when there are `queued` or `downloading` recordings present
- `FAILED` when there are only `downloaded` and `failed` recordings in the database

`stats` section contains number of recordings and their total size in GB and MB grouped by status

This API is useful for monitoring the service status and triggering alerts when something goes wrong.


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

		go run ./cmd/cli --cmd check

or like this:

		./dist/zoomrs-cli --cmd check

Available commands:
- `check` - checks the consistency of the repository: if all recordings are downloaded and if all downloaded recordings are present on the disk, also the size of each recording file is checked. Run this command periodically to make sure everything is OK. 
Run it like this:

		./dist/zoomrs-cli --cmd check
	Example output:
	```
	2023/06/19 17:15:01 [INFO]  starting CheckConsistency
	2023/06/19 17:15:01 [INFO]  Checked files: 5278
	2023/06/19 17:15:01 [INFO]  CheckConsistency: OK, 5278
	```
- `trash` - trashes recordings from Zoom Cloud. Run it like this:

		./zoomrs-cli --dbg --trash 2

	where `2` is 2 days before today, so all the recordings from the bay before yesterday will be trashed. This is designed this way to run it as a cron job every day. Cron job line example:

		00 10 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --trash 2 --config ../config/config_cli.yml >> /var/log/cron.log 2>&1

	will trash all recordings from the day before yesterday every day at 10:00 AM. `--config` option is used to specify the path to the configuration file. `--dbg` option can be used to enable debug logging. Logs are written to stdout, and redirected to `/var/log/cron.log` in the example above.

	_Note that CLI tool uses different configuration file then the server with different Zoom API credentials to avoid spoiling services's auth token when running CLI. Also, running multi-server setup you want to trash recordings only after all servers have downloaded them, so you need to run CLI tool on one of the servers, allow trashing records in CLI config and deny it in servers configs._

### Database backup
Backup database file regularly to prevent data loss. See example shell script at `dist/backup_db.sh`. It can be run as a cron job like this:

		# 0 10 * * * sh $HOME/go/src/zoomrs/backup_db.sh

## Contributing
Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change. Check the existing issues to see if your problem is already being discussed or if you're willing to help with one of them. Tests are highly appreciated.

## License
[GNU GPLv3](https://choosealicense.com/licenses/gpl-3.0/) © [Dmytro Borshchanenko](https://github.com/parMaster) 2023

## Responsible disclosure
If you have any security issue to report, contact project owner directly at [master@parMaster.com.ua](mailto:master@parMaster.com.ua) or use Issues section of this repository.

## Responsibility
The author of this project is not responsible for any damage caused by the use of this software. Use it at your own risk.

## Credits
- [lgr](github.com/go-pkgz/lgr) - simple but effective logging package
- [go-sqlite3](github.com/mattn/go-sqlite3) as a database driver
- [go-pkgz/auth](github.com/go-pkgz/auth) - powerful authentication middleware
