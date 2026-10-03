# Trustworthy tests with 80%+ coverage

**Goal:** make the test suite hermetic and deterministic, and raise total statement coverage from 27.8% to at least 80%, so later refactors can lean on it.

**Kind of change:** refactor (tests only, plus minimal testability seams in production code)

## Intent

The suite passes today but proves little. Coverage is 27.8% (1605 statements). The Zoom client has 0% because every client test calls the live Zoom API and skips without credentials. `Test_FreeUpSpace` reads the real disk's free space and bets on margins around it. Tests share fixed paths under `/tmp` while `go test ./...` runs packages in parallel, and several tests `t.Skip` on setup errors, which turns a broken setup into a green run. This plan makes every test run offline against its own temp dir, fail loudly on setup errors, and cover the real logic: the client, sync/download/cleanup in `repo`, storage, the HTTP handlers and the CLI command dispatch. Behavior stays exactly as it is. Bugs the new tests expose get pinned and logged to the backlog, not fixed.

Baseline, measured the same way the DoD measures it:

| package | stmts | covered now |
|---|---|---|
| client | 257 | 0 |
| cmd/cli | 229 | 0 |
| cmd/service | 549 | 263 |
| repo | 309 | 39 |
| storage/sqlite | 184 | 103 |
| storage/model | 39 | 26 |
| config | 20 | 15 |
| webauth | 18 | 0 |

## Decisions

- **Zoom endpoints become injectable through a variadic option**: `NewZoomClient(cfg config.Client, opts ...Option)` with `WithBaseURLs(authURL, apiURL string)`. The defaults stay `https://zoom.us` and `https://api.zoom.us/v2`, so the three existing callers don't change. Tests point the client at an `httptest.Server`. Dropped: new config keys (they would change the user-facing config for a test-only need) and swapping the `http.Client` transport (the field is unexported, so it would need a seam anyway, and rewriting hosts is harder to read).
- **Free-space check becomes an unexported func field on `Repository`**, `diskFree func(path string) (uint64, error)`, defaulting to gopsutil `disk.Usage(...).Free`. The repo test swaps in a fake whose reported free space grows as folders get deleted, so "stop once there's enough space" is tested exactly instead of by margins. Dropped: keeping the real disk with bigger margins, which is still flaky and never checks the early stop.
- **Fakes are hand-written, not moq.** The `Client` interface in `repo` has 4 methods, and a Storer stub that embeds the interface and overrides one method covers store-error paths. Happy paths use real sqlite in `t.TempDir()`. The `go:generate moq` line in `storage/storage.go` stays as it is; moq isn't installed and isn't needed.
- **Live Zoom tests move behind `//go:build integration`.** They're kept for manual runs (`go test -tags integration ./client`). Today they switch on just because `config/config_cli.yml` exists, and `DeleteRecordingsOverCapacity` there calls the real delete endpoint. That's too easy to trigger by accident once the file is restored.
- **Found bugs are pinned, not fixed** (user's choice). A test asserts today's behavior with a one-line comment saying it's wrong, and the bug gets a `docs/backlog/` item, so fixing it later is just flipping the assertion. A bug that can't be pinned without failing under `-race` (the `GetToken` mutex) gets a backlog item and no test.
- **How coverage is measured**: `go test -count=1 -coverprofile=cover.out ./...` and then `go tool cover -func=cover.out | tail -1`. That's the same per-package measurement as the baseline, with no `-coverpkg`, so cross-package calls don't inflate the number.
- **Test_CheckConsistency moves from `cmd/service` to `repo`**, where the code it tests lives.
- **Lint setup is copied from cards-v2**: a repo-level `.golangci.yml` containing only `version: "2"` (the default linters), a `make lint` target that runs a pinned golangci-lint through `go run` (`GOFLAGS= GOTOOLCHAIN=$$(go env GOVERSION) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...`), and a `Lint` step in CI that runs `make lint`. There's no global install. The file is required: without it, golangci-lint walks up and loads `/Users/gusto/go/src/.golangci.yml`, a v1 template that v2 refuses to load. zoomrs already has 0 issues under the default v2 linters, so any finding is new.

## Constraints / out of scope

- No behavior changes. The only production edits are the two seams above. Error messages, status codes and timings stay the same.
- No refactoring of the code under test, even where the tests show it's awkward. That's the next step, and these tests exist to make it safe.
- `main()` in both commands and `cmd/cli/ui.go` `ShowUI` (tview, interactive) can stay uncovered.
- No CI coverage gate for now.

## Traps

- `go test ./...` runs packages in parallel. `config/config_example.yml` points storage at `/tmp/zoomrs_test_data.db` and the repository at `/tmp`. Every test that loads the example config must override `cfg.Storage.Path` and `cfg.Storage.Repository` with `t.TempDir()` paths. `store.Cleanup` on a shared DB wipes rows another package is using.
- Hardcoded waits can't be configured: `SyncJob` retries after 30s, `CleanupJob` retries after 1 min and allows up to 10 retries, `GetAllMeetingsWithRetry` waits `30s*i`, `SyncJob` ticks hourly, and `DownloadJob` ticks every 1s and then every 1 min. Each wait is a `select` that also watches `ctx.Done()`, so reach those branches by cancelling the context. Don't add new seams for them. `CleanupJob` and the client's paging use `cfg.Client.RateLimitingDelay.*`, so set those to ~1ms in tests. (`repo/repo.go`, `client/client.go`)
- `GetAllMeetings` stops only after **two** empty 30-day windows in a row, walking back from today. A fake API that always returns meetings loops forever. (`client.GetAllMeetings`)
- `GetIntervalMeetings` pages with `next_page_token` and reuses one `*http.Request`, rewriting only `RawQuery`. A paging test must check that the second request carries the token. (`client.GetIntervalMeetings`)
- `DeleteMeetingRecordings` treats both 204 and 404 as success. Anything else is an error. (`client.DeleteMeetingRecordings`)
- `GetStats` returns an error when there are **no** downloaded records, because sqlite `GetRecordsByStatus` returns a nil slice for an empty result and `GetStats` checks `recs == nil` instead of `err`. Pin it and backlog it. (`repo.GetStats`, `sqlite.GetRecordsByStatus`)
- `LoadStorage` in both `cmd/service/main.go` and `cmd/cli/main.go` wraps errors with `%e`. That breaks `errors.Is` and prints `%!e(...)`. Assert only that an error happened. Backlog.
- `GetToken` locks a mutex created fresh on every call, so it protects nothing. A concurrent test fails under `-race`, so don't write one. Backlog only. (`client.GetToken`)
- `requestMeetingsLoaded` posts with package-level `http.Post` to every `cfg.Commander.Instances` URL and defers each `Body.Close` inside the loop. An `httptest.Server` URL in `Instances` works. The deferred close is a minor backlog item. (`repo.requestMeetingsLoaded`)
- `DownloadRecord` downloads with `grab.Get` from `record.DownloadURL` plus an access token. An `httptest.Server` works for success, non-200 and short-body cases. (`repo.DownloadRecord`)
- `statusHandler` in `cmd/service/api.go` calls the concrete `*client.ZoomClient` for the cloud report, and it calls `disk.Usage` itself. Build the server's client with `WithBaseURLs`. The disk part can stay real, since it's only reported and never compared.
- `sqlite.ListMeetings` returns only meetings that have an MP4 record (see the `seedMeeting` comment in `cmd/service/router_test.go`).

## Definition of Done

- [x] No test reads or writes a fixed path outside its own `t.TempDir()` — proof: `grep -rn '"/tmp\|os.TempDir()' --include='*_test.go' .` returns nothing, and every test that loads `config_example.yml` overrides the storage paths
- [x] No test skips on a setup error — proof: `grep -rn 't.Skip' --include='*_test.go' .` finds only files with the `integration` build tag (plus a false match on `cfg.Client.DeleteSkipped` in `repo/repo_test.go`)
- [x] Live Zoom tests don't compile in a default run — proof: `go test -count=1 -v ./client 2>&1 | grep -c Test_ZoomClient` prints `0`, and `go vet -tags integration ./client` passes
- [x] `freeUpSpace` is tested deterministically: no deletion when free space ≥ limit, deletion stops as soon as the fake reports enough space, the empty date folder is removed, the record is marked `deleted`, a missing folder is skipped, and a disk error comes back — proof: repo tests pass 20× in a row with `-count=20`
- [x] The client is covered offline for auth success and failure, token reuse, paging, rate-limit wait and ctx cancel, `GetAllMeetings` stopping, `GetAllMeetingsWithRetry` giving up on ctx cancel, the cloud report, delete (204/404/other), and `DeleteRecordingsOverCapacity` — proof: `client` coverage ≥ 85% (96.5%)
- [x] `repo` covers `SyncMeetings` filtering (min duration, important → alternative → optional fallback, empty, already-saved, `DeleteSkipped`), `DownloadOnce`/`DownloadRecord` (queued, none-queued → reset, failure marks `failed`, delete after all records load), `CleanupJob` (loaded, not loaded, force, instance errors, ctx cancel), `CheckConsistency` (missing, empty, wrong size), and `GetStats` units — proof: `repo` coverage ≥ 85% (95.8%)
- [x] `storage/sqlite` covers every `Storer` method, including `ListMeetings`, `DeleteMeeting`, `GetRecordsByStatus` and `Stats` — proof: `storage/sqlite` coverage ≥ 85% (91.3%)
- [x] `cmd/service` handlers cover their error and auth branches (`statusHandler`, `watchMeetingHandler`, `meetingsLoadedHandler`), and `LoadStorage` covers sqlite, empty and unknown types — proof: `cmd/service` coverage ≥ 80% (89.4%)
- [x] `cmd/cli` `Commander.Run` is covered for each `--cmd` value and its argument checks, against a fake Zoom server and a temp DB — proof: `Run` ≥ 80% in `go tool cover -func` (90.0%)
- [x] `storage/model`, `config` and `webauth` ≥ 80% — proof: the per-package lines in the coverage run (100% each)
- [x] Every bug the tests pinned has a `docs/backlog/` item made with the `planning:backlog` skill (at least: `GetToken` mutex, `GetStats` on an empty DB, `%e` wrapping in `LoadStorage`/config, the deferred close in the loop, plus any new ones) — proof: `ls docs/backlog/`, and each pinning test's comment says what's wrong
- [x] Total coverage ≥ 80% — proof: `go tool cover -func=cover.out | tail -1` shows `total: ... 8x.x%` (85.5%)
- [x] `make lint` works from the repo and in CI — proof: `make lint` prints `0 issues.`, and the CI workflow has a `Lint` step
- [x] The suite passes under the race detector and doesn't depend on test order — proof: `go test -race -count=3 -shuffle=on ./...` passes
- [x] ➕ Google user mapping in `webauth` moved into the named `mapGoogleUser` so it can be tested. It only runs during a real OAuth callback, and its `Email` is what the managers check trusts. This is a third seam; behavior is unchanged
- [x] ➕ CI Go bumped from 1.21 to 1.27.1 (same as cards-v2): golangci-lint v2.14.0 needs Go ≥ 1.26, and `make lint` pins the toolchain CI resolves (1.21 → the go1.24.3 from `go.mod`), so lint would fail to build on the old version

## Work order

1. Isolation first: fix temp paths and turn skips into `require` in the existing tests, then add the integration build tag, so every later step starts from a hermetic baseline.
2. Add the two seams, then write the client and repo tests. These are the biggest coverage gains.
3. Then storage, service, CLI and the small packages. Check the total, and fill the biggest gaps until it's ≥ 80%.

## Wrap-up

- [x] full test suite passes: `make test` (CI runs it with `GOFLAGS=-mod=vendor` after `go mod tidy && go mod vendor`)
- [x] linter passes: `make lint`
- [x] README.md gets a line about `go test -tags integration ./client` for the live Zoom tests
- [ ] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed && mv docs/plans/2026-09-29-trustworthy-tests.md docs/plans/completed/`)
- [ ] single commit: all changes + plan move

## Post-Completion

- Optionally run `go test -tags integration ./client` once with real credentials in `config/config_cli.yml`. Restore that file from `stash@{0}` first: `git checkout stash@{0}^3 -- config/config_cli.yml`.
