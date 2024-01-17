# Zoomrs - Zoom meetings recordings download service

### This is a binary distribution of Zoomrs project, a very brief description follows. For more information please refer to the [README](https://github.com/parmaster/zoomrs#readme) in main repository.

## Download
[RELEASES](https://github.com/parMaster/zoomrs/releases) page contains pre-built binaries for Linux, Windows and MacOS.

## Configuration
Example self-documented configuration file `config.yml` included.

## Running the service

### Foreground mode 
Plain and simple `./zoomrs` should load a default `config.yml` file and launch if everything is configured correctly:

```sh
./zoomrs
```

or specify config file and debug mode:

```sh
./zoomrs --config custom_config.yml --dbg
```

To stop the service press `Ctrl+C` (or send `SIGINT`, `SIGTERM` signal to the process)

### Systemd service
1. Configure the service and make sure it runs in foreground mode (see above).
2. Run `make deploy` to build the binary and copy everything where it belongs (see `Makefile` and `zoomrs.service` for details), enable and run the service

	```sh
	make deploy
	```

3. Run `make status` to check the status of the service

	```sh
	make status
	```

Log files are located at `/var/log/zoomrs.log` and `/var/log/zoomrs.err` by default.

### CLI Tool
CLI tool command example:

```sh
./zoomrs-cli --cmd check --config config.yml
```

Refer to the [README](https://github.com/parmaster/zoomrs#readme) in main repository to learn more about the CLI tool and its commands.

## Responsibility
The author of this project is not responsible for any damage caused by the use of this software. Use it at your own risk. However, the software is being used in production at least since May 2023 on a number of devices, processing hundreds of GB of data every day and is considered stable.
If you find a bug or have a feature request, please [open an issue](https://github.com/parMaster/zoomrs/issues/new/choose) on GitHub. Thank you!