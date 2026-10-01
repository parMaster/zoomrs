# Handlers answer correctly on edge states

**Goal:** `/status`, `/stats` and `/meetingsLoaded` give a true answer when nothing is downloaded yet or a downloaded file is gone, and the CLI's meetings-loaded request cannot hang or pile up open connections.

**Kind of change:** bug fix (four backlog items, two source files)

## Intent

Three handlers answer wrongly on an empty or partial state, and the CLI side of one of them is sloppy with its HTTP call:

| State | Today | After |
|---|---|---|
| `GET /stats` with no downloaded records | 500 | 200 with `{}` |
| `GET /status` with no downloaded video (fresh install, or audio only) | handler panics, client sees a dropped connection | 200, same body without `last_downloaded` |
| `POST /meetingsLoaded` for a `downloaded` record whose file is gone | `ok` | `pending` |
| CLI asking an instance that never answers | waits forever | error after a timeout, `CleanupJob` retries as for any instance error |
| CLI asking several instances | every response body stays open until the function returns | each body is closed before the next instance is asked |

The third row is the one that matters most: `CleanupJob` trusts `ok` and deletes the recordings from Zoom, which may be the only copy left. After the fix it sees `pending` and skips the delete. That is the intended safety effect.

All four fixes are in `cmd/service/api.go` and `repo/repo.go`, with their tests in `cmd/service/handlers_test.go` and `repo/repo_test.go`.

## Decisions

- **`GetStats` checks the error, not the slice** — a store error is returned wrapped with `%w` as today; an empty result gives an empty, non-nil map. `/stats` then answers 200 `{}`. Dropped: 204 for "nothing yet" — the handler's existing `stats == nil` branch would do that, but an empty map is a valid answer and callers shouldn't need a second code path for a fresh install.
- **`/status` leaves `last_downloaded` out when there is no downloaded video** — the same way `cloud` is left out when Zoom's report is empty. Dropped: an empty string or `null` (a monitor parsing a date would choke on either), and 204 (the rest of the status — stats, cloud, disk — is still worth reporting).
- **The "nothing downloaded" answer is not cached** — otherwise `last_downloaded` stays missing for up to 10 minutes after the first video lands.
- **`/meetingsLoaded` says `pending` for any file it cannot stat**, not only "does not exist" — a permission or I/O error also means the copy can't be confirmed, and the caller is about to delete the original. A `downloaded` record with an empty path falls under the same rule.
- **`Repository` gets its own HTTP client with a 30-second timeout**, set in `NewRepository`, used by `requestMeetingsLoaded`. 30 seconds matches the service's `WriteTimeout`: the server cuts a longer answer anyway. It is a field so tests can swap it, the same way `diskFree` is. Dropped: a config option (nobody needs to tune it), and threading `ctx` into `requestMeetingsLoaded` (changes the signature and its tests for little gain once the wait is bounded).
- **The response body is closed inside the loop on every path** — success, non-200 and decode error.

## Constraints / out of scope

- Only `cmd/service/api.go`, `repo/repo.go`, their two test files and `README.md` change. Parallel backlog work owns `cmd/service/main.go`, `cmd/cli/*` and `config/config.go`.
- No config options are added; `requestMeetingsLoaded` keeps its signature.
- Response shapes for the normal states don't change.
- `/meetingsLoaded` keeps answering `ok` for an empty list and 500 for a bad request body.
- `CheckConsistency` and `meetingRecordsLoaded` are not touched, though they do similar file and status checks.
- `statsHandler` still doesn't log the error behind its 500.

## Traps

- sqlite returns a **nil** slice with a nil error for an empty result, and `Stats` returns an empty **non-nil** map on an empty database. So on a fresh install `/status` does not take the 204 branch — it runs on to the `meetingsLoaded[0]` line (`storage/sqlite/storage.go`, `GetRecordsByStatus`, `ListMeetings`, `Stats`).
- `ListMeetings` only returns meetings with a downloaded record whose extension is `MP4`. A database full of downloaded audio still gives an empty list, so `last_downloaded` really means "latest meeting with a downloaded video" (`storage/sqlite/storage.go`, `ListMeetings`).
- The `/status` cache holds a `model.Meeting` value and the hit path type-asserts it; whatever is stored under `lastDownloadedMeeting` must stay that type (`cmd/service/api.go`, `statusHandler`).
- After the index line, `/status` still calls Zoom for the cloud report and reads disk usage. The pinning test calls the handler directly with a stubbed `ListMeetings`; once it stops panicking it runs that whole tail, so it needs the fake Zoom server and a real repository dir — `newTestServerWithZoom` gives both (`cmd/service/handlers_test.go`, `TestStatusHandler`).
- `statsHandler` answers 204 when `GetStats` returns a nil map. Returning nil for "no records" would turn the 500 into a 204, not into `{}` (`cmd/service/api.go`, `statsHandler`).
- `TestGetStats/store error` asserts `ErrorIs(err, errBoom)`, so the wrap must stay `%w` (`repo/repo_test.go`).
- The seeded test meeting keeps its files directly in the repository dir (`recM4A.m4a`), and the "wrong size" and "missing file" cases of `TestMeetingsLoadedHandler` work by rewriting or removing that file.
- `freeUpSpace` marks the records it removes as `deleted`, so those meetings already answer `pending` by status. The missing-file fix only changes the answer for files that vanished behind the service's back.
- `TestRequestMeetingsLoaded` and `TestCleanupJob` use real `httptest` servers and build the repository through `newTestRepo`; a repository built any other way in tests would have a nil client.
- `CleanupJob` retries an instance error up to 10 times, one minute apart. With a 30-second timeout a dead instance now costs about 15 minutes before the job gives up, instead of hanging (`repo/repo.go`, `CleanupJob`).

## Definition of Done

- [x] The four pinning tests are flipped to assert the correct behavior and fail on the current code — proof: `go test ./cmd/service/ ./repo/` fails in exactly `TestGetStats`, the `/stats` no-downloads test, `TestStatusHandler` and `TestMeetingsLoadedHandler` before any fix. Test names and `BUG:` comments are updated to say what is now expected.
- [x] `GetStats` with no downloaded records returns an empty map and no error — proof: the flipped `TestGetStats` case.
- [x] `GetStats` returns the store's error even when the store also returns rows — proof: a test with a stub returning both asserts `ErrorIs`.
- [x] `/stats` with nothing downloaded is 200 with body `{}` — proof: the flipped handler test, through the router.
- [x] `/status` on an empty database is 200 with `status`, `stats` and `storage`, and no `last_downloaded` key — proof: test through the router.
- [x] `/status` with only audio downloaded is 200 without `last_downloaded` — proof: test through the router.
- [x] `last_downloaded` appears on the next request after the first video is downloaded — proof: a test asks `/status` with no video, saves a downloaded MP4 record, asks again on the same server and sees the date.
- [x] `/meetingsLoaded` answers `pending` when a downloaded record's file is missing — proof: the flipped `TestMeetingsLoadedHandler` case.
- [x] `/meetingsLoaded` answers `pending` for a downloaded record with an empty path — proof: test.
- [x] `CleanupJob` does not delete from Zoom when the instance's file is missing — proof: already covered by `TestCleanupJob` "keeps meetings an instance is still loading", which must pass unchanged.
- [x] An instance that doesn't answer makes `requestMeetingsLoaded` return an error instead of blocking — proof: a test with a stalled `httptest` server and a shortened client timeout gets an error naming the instance, well inside the test's own deadline.
- [x] Each instance's response body is closed before the next instance is asked, and on the error paths — proof: a test installs a transport that records body closes and asserts it for the ok, non-200 and garbled-body cases.
- [x] Existing `TestRequestMeetingsLoaded` and `TestCleanupJob` cases pass without edits to their assertions — proof: `go test -race ./repo/`.
- [x] `README.md` says what the three endpoints answer on these states (`/stats` → `{}`, `/status` without `last_downloaded`, `/meetingsLoaded` → `pending` for a missing file) — proof: read the API section.
- [x] All four backlog items are removed in the final commit — proof: `git rm docs/backlog/get-stats-errors-on-empty-db.md docs/backlog/status-panics-without-downloaded-video.md docs/backlog/meetings-loaded-ignores-missing-files.md docs/backlog/meetings-loaded-defers-close-in-loop.md`.

## Work order

1. Flip the four pinning tests and see them fail.
2. Fix `GetStats`, `/status` and `/meetingsLoaded`.
3. Add the timeout and body-close tests, then the client field and the loop fix.

## Wrap-up

- [x] branch is not behind its remote (`git fetch && git status`)
- [x] full test suite passes: `make test`, and `go test -race ./...`
- [x] linter passes: `make lint`
- [x] README.md updated (see DoD)
- [x] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed && mv docs/plans/2026-10-01-handlers-answer-correctly-on-edge-states.md docs/plans/completed/`)
- [x] single commit: all changes + plan move + backlog removals
