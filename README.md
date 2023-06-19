# Zoomrs - Zoom meetings recordings download service

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
See `config/config_example.yaml` for example configuration file, available options and their descriptions. 

### Systemd service
1. Clone the repository from GitHub `git clone https://github.com/parMaster/zoomrs.git`
2. Edit `config/config.yaml` to your liking, use `config/config_example.yaml` as a reference
3. Run `make deploy` to build the binary and copy everything where it belongs (see `Makefile` for details), enable and run the service
4. Run `make status` to check the status of the service

Log files are located at `/var/log/zoomrs.log` and `/var/log/zoomrs.err` by default.

### Foreground mode
1. Repeat steps 1 and 2 from the previous section
2. Run `make run` to build the binary and run it in foreground mode
3. To stop the service press `Ctrl+C`

## Usage
### Web frontend
Web frontend is available at `http://localhost:8080` by default. You can change the port in the configuration file.

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
> go run ./cmd/cli --cmd check

or like this:
> ./dist/zoomrs-cli --cmd check

Available commands:
- `check` - checks the consistency of the repository: if all recordings are downloaded and if all downloaded recordings are present on the disk, also the size of each recording file is checked. Run this command periodically to make sure everything is OK. 
Run it like this:
> ./dist/zoomrs-cli --cmd check

Example output:
```
2023/06/19 17:15:01 [INFO]  starting CheckConsistency
2023/06/19 17:15:01 [INFO]  Checked files: 5278
2023/06/19 17:15:01 [INFO]  CheckConsistency: OK, 5278
```
- `trash` - trashes recordings from Zoom Cloud. Run it like this:
> ./zoomrs-cli --dbg --trash 2

where `2` is 2 days before today, so all the recordings from the bay before yesterday will be trashed. This is designed this way to run it as a cron job every day. Cron job line example:
> 00 10 * * * cd $HOME/go/src/zoomrs/dist && ./zoomrs-cli --trash 2 --config ../config/config_cli.yml >> /var/log/cron.log 2>&1

will trash all recordings from the day before yesterday every day at 10:00 AM. `--config` option is used to specify the path to the configuration file. `--dbg` option can be used to enable debug logging. Logs are written to stdout, and redirected to `/var/log/cron.log` in the example above.

## Contributing
Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change. Check the existing issues to see if your problem is already being discussed or if you're willing to help with one of them. Tests are highly appreciated.

## Credits
- [lgr](github.com/go-pkgz/lgr) - simple but effective logging package
- [go-sqlite3](github.com/mattn/go-sqlite3) as a database driver
- [go-pkgz/auth](github.com/go-pkgz/auth) - powerful authentication middleware
