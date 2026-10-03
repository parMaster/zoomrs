# CLI sync downloads only the requested day

**Goal:** `zoomrs-cli --cmd sync --days N` downloads and requeues only the records of the day it was asked to sync.

**Kind of change:** bug fix

## Intent

The CLI `sync` command lists the meetings of one day, saves the new ones, and then downloads.
The download phase is not tied to that day: it takes whatever is queued in the whole database,
oldest first, and when the queue runs empty it puts every `failed` and `downloading` record in
the table back in the queue (GitHub issue #19). A run for yesterday therefore also downloads a
ten-day-old backlog, retries old failures, and can requeue a record the service is downloading
at that moment.

After this change the CLI's download phase only touches records that belong to the meetings of
the requested day. Everything else in the database stays as it was.

```
before:  sync day N ──▶ download ANY queued record ──▶ queue empty ──▶ requeue ALL failed/downloading ──▶ ...

after:   sync day N ──▶ download queued records of day N ──▶ none left ──▶ requeue day N failed/downloading ──▶ ...
                        (other days' records: never read, never changed)
```

## Decisions

- **"The requested day" means the meetings Zoom lists for that day** — the scope is the set of
  meeting UUIDs returned by the day's listing, not a date filter on the stored start time.
  The listing already defines the day the user asked for; a date filter would have to guess
  the time zone Zoom used for `from`/`to` against start times stored without a zone. Dropped
  for that reason.
- **Meetings of that day that were saved by an earlier run are in scope** — if yesterday's
  meeting is already in the database with queued or failed records, this run downloads them.
  The fix is about other days, not about "only what this run inserted".
- **Requeue stays, scoped to the day** — `failed` and `downloading` records of the day's
  meetings go back to `queued` when the day's queue runs empty, exactly as today's whole-table
  requeue does.
- **No limit on the number of attempts** — Zoom can take hours to finish encoding a recording,
  so a run started at 0:30 may only succeed at 2:30. The 30-second wait between failed
  attempts and the 12-hour cap on the download phase stay as they are.
- **The service is not changed** — its download job keeps taking any queued record and
  requeueing the whole table. The scoped behaviour is added next to the existing one, not
  instead of it.
- **Trash/delete in Zoom after a meeting is fully downloaded stays** — same rule as the
  service's download path.

## Constraints / out of scope

- `DownloadJob` and `DownloadOnce` behave exactly as before for the service.
- The exit condition of the CLI stays: it ends cleanly once the day has nothing queued and the
  requeue found nothing to put back.
- A day with no meetings downloads nothing and exits cleanly.
- Out of scope: stopping two processes from downloading the same record at once; a retry
  limit or backoff; making an in-flight download obey the 12-hour cap.

## Traps

- `DownloadOnce` does three things in one call: picks a record, requeues on an empty queue
  (returning `ErrNoQueuedRecords`), and trashes the meeting in Zoom once all its records are
  downloaded. The CLI loop relies on all three, and on seeing `ErrNoQueuedRecords` twice in a
  row to exit (`repo/repo.go`, `DownloadOnce`; `cmd/cli/main.go`, `sync` case).
- An empty scope must mean "nothing", not "no filter". A day with no meetings, or an empty
  UUID list reaching a query, must not fall back to the whole table.
- The day's listing includes meetings that sync skipped (too short, no syncable record types).
  They have no rows in the database; a UUID with no records is normal, not an error
  (`repo/repo.go`, `SyncMeetings`).
- `records.meetingId` holds the meeting UUID, the same value as `meetings.uuid`
  (`storage/sqlite/storage.go`, `GetRecords`).
- Meeting UUIDs can contain `/`, `+` and `=`. They must reach SQL as bound parameters.
- The queue is ordered by `startTime, id`; keep that order inside the day so runs stay
  predictable (`storage/sqlite/storage.go`, `GetQueuedRecord`).
- `Storer` is an interface with a hand-written stub in `repo/repo_test.go` (`stubStore`, which
  embeds the real store) and whole-table assertions in `storage/sqlite/storage_test.go` for
  `GetQueuedRecord` and `ResetFailedRecords`. Those tests describe the service's behaviour and
  must keep passing unchanged.
- `TestRun_Sync` in `cmd/cli/main_test.go` pins the happy path (download, then one delete in
  Zoom) and the error text `downloading terminated` when a failing download is canceled.
- The CLI test helper serves one meeting and builds the database fresh per test; reproducing
  the bug needs a second, older meeting with queued/failed records put in the store before
  `Run` is called.

## Definition of Done

- [x] The bug is reproduced first — proof: a CLI test with an older meeting's record queued in
      the database and a day's listing that does not include it; before the fix the old record
      ends `downloaded`, after the fix it is still `queued`.
- [x] The day's records are downloaded and the meeting is trashed in Zoom — proof: the existing
      `TestRun_Sync` happy path passes unchanged.
- [x] Old `failed` and `downloading` records are not requeued — proof: a CLI test seeding one
      of each for a meeting outside the day; both keep their status after the run.
- [x] A failed record of the requested day is requeued and retried — proof: a CLI test where
      the file server fails the first attempt and serves the second; the record ends
      `downloaded`.
- [x] A meeting of the requested day already saved by an earlier run gets its queued records
      downloaded — proof: a CLI test seeding that meeting before the run.
- [x] A day with no meetings leaves a non-empty queue untouched and exits without error —
      proof: a CLI test with an empty listing and a queued record in the database.
- [x] The store's scoped lookups handle UUIDs with `/`, `+`, `=` and an empty scope — proof:
      storage tests for both the "next queued record" and the "requeue" operations.
- [x] The service's behaviour is unchanged — proof: existing `repo` and `storage/sqlite` tests
      for `DownloadOnce`, `DownloadJob`, `GetQueuedRecord` and `ResetFailedRecords` pass
      without edits to their assertions.

## Wrap-up

- [x] full test suite passes: `make test` (and `go test -race ./repo/... ./cmd/cli/...`)
- [x] linter passes: `make lint`
- [x] README.md: the `--cmd sync --days` section says the download is limited to that day
- [x] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed && mv docs/plans/2026-10-03-cli-sync-downloads-only-requested-day.md docs/plans/completed/`)
- [x] single commit: all changes + plan move

## Post-Completion

- This branch is stacked on `refresh-zoom-token-on-401`. Open its PR after that one merges, or
  with that branch as the base.
- Close GitHub issue #19 once merged.
