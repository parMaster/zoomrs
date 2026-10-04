# CLI: 30-day window, per-meeting trash, run lock

**Goal:** make `zoomrs-cli --cmd sync` and `--cmd trash` safe to run from a timer with no arguments: they cover the last 30 days, trash only meetings every instance confirms, never run twice at once, and stop retrying old failures.

**Kind of change:** feature (steps 2 and 3 of issue #30; timer units and removal of the service jobs are separate, see out of scope)

## Intent

Today `sync` handles one day (`--days`, default 1) and `trash` needs `--trash N`, so a missed run loses a day. `trash` is also all or nothing: one pending meeting or one unreachable instance blocks every deletion, and the download step can trash a meeting on its own. A timer that fires after a power cut needs to catch up without help and delete nothing that isn't confirmed.

```
before:  sync  --days N (default 1) -> one day        trash --trash N (required) -> one day, all or nothing
after:   sync / trash (no flag)     -> last 30 days, one listing call
         sync / trash --days N      -> one day (as before)
         trash --trash N            -> deprecated alias of --days N
         trash: asks every instance per meeting -> trashes the meetings ALL of them confirm

/meetingsLoaded  before: {"result":"ok"|"pending"}            (first pending meeting ends the check)
                 after:  {"result":"ok"|"pending", "loaded":[uuid,...]}   (every meeting is checked)
```

## Decisions

- **Unset `--days` means the 30-day window.** The default becomes a "not set" sentinel instead of 1. `--days 0` stays valid (today). Dropped: a separate `--window` flag.
- **`--trash N` stays as a deprecated alias.** It maps to `--days N` and logs a deprecation warning. If both are given and differ, exit with an error.
- **`GetMeetings(daysAgo)` stays for the single-day case; the window uses `GetIntervalMeetings(from, to)` directly.** The trash code takes a from/to pair instead of `daysAgo`, so one function serves both modes.
- **`/meetingsLoaded` adds `loaded`, the UUIDs confirmed downloaded.** `result` keeps its meaning (`ok` only when all are loaded) for older callers. A meeting is confirmed only when it has records, all of them are `downloaded`, and each file exists at the expected size (the handler's existing `os.Stat` and size checks stay; an unreadable file is not a confirmed copy). An older instance that returns no `loaded` list counts as: `ok` confirms every meeting asked, `pending` confirms none.
- **Trash is per meeting.** A meeting is trashed only if every configured instance lists it in `loaded`. With no instances configured, trash nothing (today `requestMeetingsLoaded` errors on that; "every instance confirms" must not become vacuously true). `--force` still trashes everything in range without asking any instance, but now requires an explicit `--days` (or `--trash`), so it can never reach 30 days of meetings, today's included. This tightens the earlier "`--force` unchanged" choice.
- **Unreachable instance.** Retries run in rounds: ask every instance, wait a minute, ask again only those that did not answer, up to 10 retries. Instances that answered keep their answer, so the wait stays under about 10 minutes however many are down. After that, trash nothing that an unanswered instance has not confirmed (so, nothing), log it, and leave it for the next daily run, which covers the same window. Meetings confirmed by the instances that answered are not trashed on a partial answer.
- **No delete without confirmation.** The download step no longer trashes or deletes (`trash_downloaded` and the `delete_downloaded` use in the download step are removed). Only the trash command removes a downloaded meeting. `delete_downloaded` stays as the permanent-vs-trash switch for the trash command and `delete_skipped`. Removing `trash_downloaded` from the config struct is safe for a yaml that still sets it (`config.go` unmarshals non-strictly, so the key is ignored), but the old behavior silently stops; log a warning when it is set.
- **Requeue rule.** `downloading` records of listed meetings are requeued as before, with no age limit: a record stuck in `downloading` means a run died mid-download (power cut), not a real failure. This is only safe while one writer runs, which the lock guarantees for CLI runs; until `download_job` is off on a server, the service can still race the CLI. `failed` records are requeued only if the record's `startTime` is within the last 3 days, and each at most once per run. Without that cap, a recent record that fails every time loops requeue → download → fail until the 12-hour limit, holding the lock. Older ones stay `failed`.
- **Exit summary.** However a `sync` run ends (clean finish, the 12-hour timeout, an error or a signal), it logs the `failed` records of the listed meetings that were not requeued (meeting topic, record id, start time). Nothing is printed if there are none.
- **Run lock.** Non-blocking `flock` on `<file part of the storage DSN>.lock`. The shipped configs use a SQLite URI (`file:/data/_db/main.db?mode=rwc&...`), so strip `file:` and the `?query` first; the lock is then always next to the real db file, whatever directory the CLI runs from. If it is held, the run logs it and exits without touching Zoom. The PID is written into the lock file so the message can name the holder. Taken by `sync`, `trash` and `cloudcap`; not by `check` or the UI. It releases itself when the process dies. Dropped: a lock row in SQLite (needs cleanup after a crash).
- **Version.** The CLI flags stay backwards compatible, but the `trash_downloaded` removal changes behavior for servers that set it; decide at release whether that warrants a major bump, and say so in the README.

## Constraints / out of scope

- Not in this plan: timer units in `deploy/`, switching each server from the service jobs to the CLI timers (done live, with you, after the tests pass), removing `SyncJob`/`DownloadJob` and `sync_job`/`download_job`, the README multi-server rewrite, `Persistent=` testing on staging.
- The service keeps running its jobs as before, except that the download step stops trashing (a shared code path).
- With the default window there is no grace day: `trash` can trash yesterday's meetings once every instance confirms them. The README must say so and that `--days 2` keeps the old one-day behavior.
- Exit codes stay as they are (see Traps).

## Traps

- `main()` in `cmd/cli/main.go` logs the error from `Commander.Run` and exits 0. A held lock or a failed run looks like success to a timer.
- `--days` has `env:"DEBUG"`, the same variable as `--dbg`. Setting `DEBUG=true` today feeds a bool into the int flag. It needs its own env name (or none).
- `CleanupJob` (`repo/repo.go`) retries the Zoom listing forever on error, but its `meetingsLoaded` retry counter is shared across loop turns and gives up after 10. The new per-instance retry replaces the counter; the listing retry must stay.
- `requestMeetingsLoaded` returns at the first instance that is not `ok` or fails (`repo/repo.go`), so later instances are never asked. Per-meeting trash needs an answer from every instance.
- The `/meetingsLoaded` handler (`cmd/service/api.go`) writes `pending` and returns at the first meeting with no records or a record that is not `downloaded`. It must check all meetings. Meetings with no records (short or empty ones kept on Zoom when `delete_skipped` is off) are never confirmed, so they stay on Zoom; that is intended, say so in the README.
- `ResetFailedRecordsOf` (`storage/sqlite/storage.go`) resets `failed` and `downloading` in one statement with no age filter. The 3-day rule needs a separate condition on `records.startTime`, a text column stored as local `time.DateTime`, so compare in that format.
- `GetIntervalMeetings` (`client/client.go`) sends `from`/`to` as dates and pages by 300 with `RateLimitingDelay.Medium` between pages. Zoom accepts at most one month per query, so 30 days sits at the limit. `GetAllMeetings` already uses 30-day chunks for the same reason.
- `cmd/cli/main_test.go`, `repo/repo_test.go` and `cmd/service/handlers_test.go` pin the one-day sync flow, the all-or-nothing cleanup and the download-step delete; expect to change them.
- The test configs build the storage path as `file:<tmp>/x.db?mode=rwc&...` (`cmd/cli/main_test.go`); the lock tests must use the same shape.
- `SyncMeetings` skips meetings already in the db, so a 30-day window re-lists but does not re-save. Harmless: only `queued` records are fetched.

## Definition of Done

- [x] `sync` with no `--days` lists the last 30 days with one `GetIntervalMeetings` call and downloads queued records of those meetings only — proof: CLI test with a fake client asserting the from/to range and that records of unlisted meetings are untouched.
- [x] `sync --days N` and `trash --days N` still handle exactly one day — proof: tests for N = 0 and N = 2.
- [x] `--trash N` works as an alias for `--days N` and logs a deprecation warning; conflicting values are rejected — proof: flag-parsing tests.
- [x] `--days` no longer reads `DEBUG` — proof: test with `DEBUG=true` set parses without error and leaves days unset.
- [x] `/meetingsLoaded` returns `loaded` with every confirmed UUID even when others are pending, and `result` is unchanged — proof: handler test with a mix of downloaded, queued, record-less, missing-file and wrong-size meetings.
- [x] With no instances configured, trash deletes nothing and logs why; `--force` with an explicit `--days` still deletes — proof: repo test for both.
- [x] Trash deletes exactly the meetings all instances confirm: one pending meeting no longer blocks the others, a meeting one instance lacks is kept — proof: repo test with two fake instances answering different lists.
- [x] An instance that answers `result` only (older version) confirms all on `ok` and none on `pending` — proof: test with a legacy-style fake instance.
- [x] An unreachable instance is retried in rounds, 10 retries a minute apart, and only the instances that did not answer are asked again; then nothing is trashed and the run ends with a log line; the next run tries again — proof: test with an injectable wait, one fake that never answers and one that answers, counting calls to each.
- [x] `trash --force --days N` trashes everything for that day without asking any instance; `--force` without `--days` or `--trash` is rejected — proof: tests with instances that would answer `pending`, and a flag-validation test.
- [x] The download step never trashes or deletes, whatever `trash_downloaded` and `delete_downloaded` say — proof: `DownloadOnce` test with both set; a yaml that still sets `trash_downloaded` loads with a warning.
- [x] `failed` records older than 3 days are not requeued; younger `failed` and all `downloading` of listed meetings are — proof: storage test with records on both sides of the cutoff.
- [x] A recent `failed` record that fails again is requeued once per run, not in a loop — proof: test with a download that always fails, asserting the attempt count and that the run ends.
- [x] The sync run ends with a summary of non-requeued `failed` records on every exit path (finish, timeout, error), and prints nothing when there are none — proof: test capturing the log output for both cases and for a timeout.
- [x] A second CLI run while one holds the lock exits without calling Zoom; a run after a killed process works; the lock file sits next to the db for a `file:...?mode=rwc` DSN, whatever the working directory — proof: tests using a URI-style path.
- [x] README documents the default window, `--days`, the `--trash` alias, per-meeting trash and the new `/meetingsLoaded` response, the lock, the no-grace-day note, and that record-less meetings stay on Zoom — proof: read the diff.
- [ ] Zoom limits checked live — proof: run `sync` with the default window against staging; record the page count and any 429 or `Retry-After`. If limited, split the window into chunks of a few days (add as a ➕ item).

## Work order

1. Flags and window selection (`--days`, alias, env fix).
2. `/meetingsLoaded` response, then per-instance, per-meeting trash with the retry.
3. Remove the download-step trash and `trash_downloaded`.
4. 3-day `failed` requeue and exit summary.
5. Run lock.
6. README, then the live check on staging.

## Wrap-up

- [x] full test suite passes: `make test`
- [x] linter passes: `make lint`
- [x] README.md updated (behavior, flags and API changed), and the `trash_downloaded` / `delete_downloaded` / `delete_skipped` comments in `config/*.yml` and `dist/config.yml`
- [ ] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed && mv docs/plans/2026-10-04-cli-window-and-per-meeting-trash.md docs/plans/completed/`)
- [ ] single commit: all changes + plan move

## Post-Completion

- Run the live check on staging before moving any server to the default window.
- Timer units, moving each server off the service jobs, and removing `SyncJob`/`DownloadJob` are the next plan.
