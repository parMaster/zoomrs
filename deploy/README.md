# Deploying zoomrs on a VPS

Everything here runs on the server, from this directory, inside the cloned repo.

| File | Purpose |
|---|---|
| `Makefile` | `deploy`, `status`, `start`, `stop` |
| `zoomrs.service` | systemd unit template (`%USER%` is filled in by `make deploy`) |
| `backup_db.sh` | database backup, meant for cron |

`dist/` is a different thing: the template for the release tarballs and the build output.

## First time

1. Create `/etc/zoomrs/config.yml` (start from `config/config_example.yml`). Deploy never touches it.
2. Make sure `storage.path` and `storage.repository` in that config point into `/data` (the database in `/data/_db`). The unit's sandbox only lets the service write there; to use other paths, change `ReadWritePaths` in `zoomrs.service`.
3. `make deploy`

## Updating

```sh
cd ~/go/src/zoomrs && git pull && cd deploy && make deploy
```

## Sandbox

The service runs as your login user, so the unit takes away what a bug in it could reach: sudo (`NoNewPrivileges`), your home directory and SSH keys (`ProtectHome`), and all of the filesystem except `/data` and its log files (`ProtectSystem=strict` + `ReadWritePaths`). It also drops all capabilities and restricts syscalls, address families and kernel interfaces.

Outside the sandbox and unaffected: `backup_db.sh` (cron), `zoomrs-cli`.

### Checking it

```sh
systemd-analyze verify /etc/systemd/system/zoomrs.service
systemd-analyze security zoomrs.service      # exposure level at the bottom, 1.5 OK on the last check
systemctl status zoomrs.service              # must be active (running)

# what the service sees: $HOME hidden, /etc read-only, /data writable
sudo nsenter -t "$(systemctl show -p MainPID --value zoomrs.service)" -m \
  sh -c 'ls /home; touch /etc/x; touch /data/x && rm /data/x'
```

After a deploy, also check that the web UI answers, a sync reaches Zoom, a download lands in `/data`, and `/var/log/zoomrs.err` has no `permission denied`. If something is denied, add its path to `ReadWritePaths`.
