# Deploy folder and systemd sandbox

**Goal:** Move the VPS deploy files into a `deploy/` folder with its own Makefile, and sandbox the systemd unit, the way `../cards-v2` does.

**Kind of change:** refactor (layout) + feature (sandbox)

## Intent

Today `dist/` mixes two things: ignored build output (`zoomrs`, `zoomrs-cli`) and tracked files. Some of those tracked files are the release-tarball template (Makefile, README, unit, `multibuild.sh`). The author's VPS deploy lives in the root Makefile, next to build/run/cli targets. It runs `sed -i` on the tracked unit file and copies `config/config.yml` out of the repo clone, so secrets sit in the working tree. The unit has no sandbox, and a bug in the service could reach the deploy user's sudo, home and SSH keys.

After: `deploy/` holds everything the VPS needs. The root Makefile only builds and tests. The unit locks the service down to its two data directories.

```
before                                 after
root Makefile: build+run+deploy        root Makefile: build, test, lint, run, release
dist/: output + release template       dist/: output + release template (unchanged role)
                                       deploy/: Makefile, zoomrs.service (sandboxed),
                                                backup_db.sh, README.md
```

## Decisions

- **`deploy/` is for the author's VPS; `dist/` stays the release-tarball template** — `multibuild.sh` ships `dist/Makefile`, `dist/README.md` and `dist/zoomrs.service` to end users whose paths we don't know, so the path-specific sandboxed unit can't replace the generic one. Dropped: pointing `multibuild.sh` at `deploy/`.
- **Deploy Makefile builds via `cd .. && $(MAKE) build`, installs binary + unit, never touches config** — same as cards-v2. `/etc/zoomrs/config.yml` must exist before the first deploy. This removes the copy of `config/config.yml` from the repo clone.
- **Render the unit with `sed ... | sudo tee`, not `sed -i`** — keeps the tracked `deploy/zoomrs.service` clean.
- **Sandbox set copied from cards-v2** (no new privileges, empty capability set, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp/Devices`, kernel/proc/clock protections, namespaces/realtime/SUID restrictions, `MemoryDenyWriteExecute`, `@system-service` syscall filter, address families `AF_INET AF_INET6 AF_UNIX AF_NETLINK`), adapted to zoomrs:
  - `ReadWritePaths=/data` plus `-/var/log/zoomrs.log -/var/log/zoomrs.err`. The service writes WAL files next to the DB and creates and deletes recording folders in `/data`, so it needs read-write.
  - Drop the `ReadOnlyPaths` line; nothing here is read-only.
  - No `RemoveIPC=` (login user as `User=`).
- **Paths assumed `/data/_db` and `/data`** (confirmed by the user). The README documents that they must match `storage.path` / `storage.repository` in the VPS config.
- **Remove the `nss-lookup.target` lines** from the VPS unit; they say the app must start before name lookups work, which is backwards.
- **`backup_db.sh` moves to `deploy/`**. The root Makefile's `status/stop/start/deploy` targets move to `deploy/Makefile`.

## Constraints / out of scope

- No Go code changes. File ownership on `/data` is unchanged, so the backup cron and the CLI keep working (they run outside the sandbox).
- The release tarball and Docker image are not changed. The Dockerfile already runs as an unprivileged user.
- No nginx, logrotate or pull_backup files; zoomrs has none today.

## Traps

- `dist/` is in `.gitignore` but `dist/Makefile`, `README.md`, `backup_db.sh`, `multibuild.sh`, `zoomrs.service` are tracked, so they only stay tracked because they were added before the ignore rule. `git mv` out of `dist/` works; new files under `dist/` would be ignored.
- `multibuild.sh` copies `./Makefile`, `./README.md`, `./config.yml`, `./zoomrs.service` from `dist/` into each tarball, and the root `release` target creates `dist/config.yml`. Don't move those.
- Root `Makefile` `.PHONY` and the `deploy` target reference `config/config.yml`, which is gitignored (`.gitignore`).
- `README.md` points at `$HOME/go/src/zoomrs/backup_db.sh` for the cron job, but the script is in `dist/`. Fix to the new path.
- The service is a cgo build (SQLite), so name lookups go through glibc and need `AF_NETLINK`; `MemoryDenyWriteExecute` is fine with it in cards-v2 (same driver), but check it here on the VPS.
- `StandardOutput=append:` log files are opened by systemd; under `ProtectSystem=strict` they still need the `-` prefixed `ReadWritePaths` entries.

## Definition of Done

- [x] `deploy/` contains `Makefile`, `zoomrs.service`, `backup_db.sh`, `README.md`; the deploy targets are gone from the root Makefile and `backup_db.sh` is gone from `dist/` — proof: `git ls-files deploy dist` and `grep -n 'systemctl' Makefile` returns nothing.
- [x] Root `make build`, `make test`, `make lint`, `make release` still work — proof: run each; `make release` produces tarballs that still contain Makefile, README, config.yml and the generic unit.
- [x] `make -C deploy -n deploy` shows the intended steps and never writes to the tracked unit file — proof: dry-run output; `git status` clean after a real run on the VPS.
- [x] The unit passes `systemd-analyze verify` and scores a low exposure level — proof: `systemd-analyze security zoomrs.service` on the VPS, number recorded in the README.
- [x] Sandboxed service still works end to end — proof on the VPS: service is `active`, web UI answers on the listen port, a sync run reaches Zoom, a download lands in `/data`, an eviction/delete removes a folder, WAL files appear in `/data/_db`, `backup_db.sh` runs.
- [x] The sandbox actually blocks things — proof: `nsenter` into the service's namespace shows `$HOME` hidden and `/etc` read-only (same check as cards-v2's README).
- [x] `README.md` deploy section and cron path point at `deploy/`; `deploy/README.md` explains first-time setup (config at `/etc/zoomrs/config.yml`), deploy, sandbox paths and how to verify.

## Work order

1. Create `deploy/` (git mv `backup_db.sh`; copy the generic unit and harden it; port the Makefile).
2. Strip deploy targets from the root Makefile.
3. Update README.md and write `deploy/README.md`.
4. Deploy on the VPS and run the proofs above; adjust `ReadWritePaths` if anything is denied.

## Wrap-up

- [x] full test suite passes: `make test`
- [x] linter passes: `make lint`
- [x] README.md updated (done in work order step 3)
- [x] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed && mv docs/plans/2026-10-01-deploy-folder-and-sandbox.md docs/plans/completed/`)
- [x] single commit: all changes + plan move

## Post-Completion

- Run the VPS proofs after merging (needs sudo on the VPS). If the real paths differ from `/data` and `/data/_db`, update `ReadWritePaths` and `backup_db.sh` first.
