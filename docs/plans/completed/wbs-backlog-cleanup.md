# WBS: Backlog cleanup

**Scope source:** ad hoc — the 8 items in `docs/backlog/` as of 2026-10-01
**Definition of Done (epic-level):** `docs/backlog/` is empty — every item is fixed, its pinning test asserts the correct behavior, and `go test -race ./...` and `golangci-lint run ./...` pass.

---

## Scope

| Ticket | Title |
|--------|-------|
| `errors-formatted-with-percent-e` | Errors formatted with %e instead of %w / %v |
| `get-stats-errors-on-empty-db` | GetStats treats "no downloaded records" as an error |
| `get-token-mutex-guards-nothing` | GetToken's mutex is created per call and guards nothing |
| `meetings-loaded-defers-close-in-loop` | requestMeetingsLoaded defers Body.Close inside the instances loop (`worth: later`) |
| `meetings-loaded-ignores-missing-files` | meetingsLoaded answers "ok" for a downloaded record whose file is gone |
| `server-never-shuts-down` | HTTP server is never shut down on cancel |
| `status-panics-without-downloaded-video` | /status panics when no meeting has a downloaded video |
| `zoom-error-messages-print-body-reader` | Zoom client error messages print the body reader and mislabel failures |

Ticket IDs are file names under `docs/backlog/` (add `.md`).

## Chunking Decision

None of the items would produce throwaway stubs if planned alone, so the
stub-avoidance rule alone would leave all 8 ungrouped. They are grouped anyway
because each is a few lines and items in the same group edit the same source
and test files: 8 separate sessions would be mostly overhead and merge
conflicts. Grouping is by shared files, so the three iterations do not overlap
and can run in parallel.

- Iteration 1 owns `client/client.go`.
- Iteration 2 owns `cmd/service/api.go` and `repo/repo.go`.
- Iteration 3 owns `cmd/service/main.go`, `cmd/cli/*` and `config/config.go`.

## Iterations

### Iteration 1: Zoom client — token race and error messages
**Tickets:** `get-token-mutex-guards-nothing`, `zoom-error-messages-print-body-reader`
**Status:** done
**Grouping rationale:** Both fixes are in `client/client.go` and its test file. The token fix moves the mutex onto `ZoomClient`; the error-message fix rewrites the non-200 branches of `GetIntervalMeetings`, `GetCloudStorageReport` and `DeleteMeetingRecordings`, including the misleading "unable to authorize" text. Done separately they would conflict in the same file.
**INVEST notes:** No pinning test exists for the token race — a concurrent test that fails under `-race` must be written together with the fix. Otherwise clean.

### Iteration 2: Handlers answer correctly on edge states
**Tickets:** `get-stats-errors-on-empty-db`, `status-panics-without-downloaded-video`, `meetings-loaded-ignores-missing-files`, `meetings-loaded-defers-close-in-loop`
**Status:** done
**Grouping rationale:** All four are wrong answers on an empty or partial state, in `cmd/service/api.go` and `repo/repo.go`, with pinning tests in the same test files (`TestGetStats/no_downloaded_records_is_an_error`, `TestStatsHandler_NoDownloadsIs500`, `TestStatusHandler/panics_when_nothing_is_downloaded_yet`, `TestMeetingsLoadedHandler/ok_even_when_a_downloaded_file_is_missing`). `requestMeetingsLoaded` in `repo/repo.go` is the caller side of the same meetings-loaded check that `meetingsLoadedHandler` serves, so its `defer`-in-loop and missing HTTP timeout are fixed in the same pass.
**INVEST notes:** `meetings-loaded-defers-close-in-loop` is `worth: later` and has no pinning test; included because the whole backlog is in scope. The `meetingsLoaded` fix changes what `CleanupJob` sees ("pending" instead of "ok"), which is the intended safety effect.

### Iteration 3: Service lifecycle and error wrapping
**Tickets:** `server-never-shuts-down`, `errors-formatted-with-percent-e`
**Status:** done
**Grouping rationale:** Both edit `cmd/service/main.go` (`startServer`, `Run`, `NewServer`, `LoadStorage`) and its tests. The `%e` fix also touches `cmd/cli/main.go`, `cmd/cli/ui.go` and `config/config.go`, which no other iteration edits.
**INVEST notes:** `TestRun_ServesUntilCanceled` passes today only because `Run` doesn't wait for `startServer`; it needs tightening to prove the drain. The `LoadStorage` tests in both commands only check the message prefix and should assert `errors.Is` after the `%w` fix.

## Progress Log
- 2026-10-01: WBS created. 3 iterations defined.
- 2026-10-01: Iteration 1 (Zoom client — token race and error messages) kicked off in session "Spawn: zoom-client".
- 2026-10-01: Iteration 1 done — commit `4a6ef50` pushed to `trustworthy-tests`; both backlog items removed, plan in `docs/plans/completed/2026-10-01-zoom-client-token-race-and-error-messages.md`.
- 2026-10-01: Iteration 2 (Handlers answer correctly on edge states) kicked off in session "Spawn: handlers".
- 2026-10-01: Iteration 3 (Service lifecycle and error wrapping) kicked off in session "Spawn: service-lifecycle", in parallel with iteration 2 on the same branch.
- 2026-10-01: Iteration 2 done — commit `8254042` on `trustworthy-tests`; plan in `docs/plans/completed/2026-10-01-handlers-answer-correctly-on-edge-states.md`.
- 2026-10-01: Iteration 3 done — commit `f1591d1` on `trustworthy-tests` (not pushed yet); both backlog items removed, plan in `docs/plans/completed/2026-10-01-service-lifecycle-and-error-wrapping.md`. `docs/backlog/` is empty.
- 2026-10-01: Epic complete — all iterations done. No housekeeping needed; `go test -race ./...` and `make lint` pass.
