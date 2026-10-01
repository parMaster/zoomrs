# Zoom client — token race and error messages

**Goal:** One shared `ZoomClient` is safe to call from several goroutines, and a failed Zoom call says which call failed and what Zoom answered.

**Kind of change:** bug fix (two bugs, one file)

## Intent

The service's sync and download jobs share one `ZoomClient`. `GetToken` locks a mutex it creates on every call, so nothing is guarded: callers race on the token and can authorize several times at once.

Separately, the non-200 branches of `GetIntervalMeetings`, `GetCloudStorageReport` and `DeleteMeetingRecordings` format `resp.Body` (a reader) with `%s`, so logs show `message: {}` instead of Zoom's text. `GetIntervalMeetings` also reports every non-200 as "unable to authorize with account id…", which points at credentials when the cause is e.g. a 429 rate limit.

Both fixes live in `client/client.go` and `client/client_test.go`, so they ship as one change.

## Decisions

- **The mutex becomes a field of `ZoomClient`, held for the whole check-and-authorize** — concurrent callers with no valid token wait for one authorization instead of each starting their own. Dropped: `sync.Once` (can't re-run on expiry), `singleflight` (new dependency for a few lines).
- **API methods use the token `GetToken` returns** — today they discard it and read `z.token` again without the lock. After the fix nothing outside the locked section reads the field.
- **A refresh swaps in a new token value; it never rewrites the old one** — a caller that already holds a token keeps reading it while another goroutine refreshes. A failed refresh leaves the previous token untouched.
- **`Authorize` stays exported and becomes safe to call directly** — it takes the same lock. `GetToken` therefore must not call the locking version of it while holding the lock.

```
before                                   after
GetToken: lock(private mutex)            GetToken: lock(z's mutex)
  token nil/expired? -> Authorize          token nil/expired? -> authorize, swap pointer
  (Authorize rewrites *z.token in place)   return token
caller: reads z.token.AccessToken        caller: uses the returned token
        (no lock)
```

- **Error text is `<what failed>, status <code>, message: <body>`** — the body is read as raw text, capped at 1 KiB and trimmed; no JSON parsing, since a proxy or gateway may answer with HTML. One shared helper builds it for all three calls. With an empty body the message still reads cleanly.
- **`GetIntervalMeetings` says "unable to get recordings"** and drops the account id and client id — they only matter for authorization, which `Authorize` already reports.

## Constraints / out of scope

- Only `client/client.go` and `client/client_test.go` change. Other backlog work running in parallel owns `cmd/`, `repo/` and `config/`; the `Makefile` stays as is.
- Public signatures of `ZoomClient` methods don't change.
- `Authorize`'s own non-200 message is correct and stays as is (no body added).
- The `defer resp.Body.Close()` inside the paging loop of `GetIntervalMeetings` is not touched.
- `DeleteMeetingRecordings` keeps treating 204 as success and 404 as "already gone".

## Traps

- `Authorize` decodes with `json.NewDecoder(resp.Body).Decode(&z.token)`. When `z.token` is already set, the decoder fills the existing struct, so a refresh rewrites the token other goroutines are holding. Moving the mutex alone leaves this race in place (`client/client.go`, `Authorize`).
- `sync.Mutex` is not reentrant: `GetToken` calling a locking `Authorize` deadlocks (`client/client.go`, `GetToken`).
- `make test` and CI run `go test ./...` without `-race`. The new test must therefore also fail without the race detector — the count of token requests does that: `fakeZoom.tokenCalls` already counts them (`client/client_test.go`, `newFakeZoom`).
- Expiry is set to "now + expires_in − 5 minutes", so a fake token with `expires_in` under 300 is expired on arrival and every `GetToken` re-authorizes. That is the cheap way to exercise refresh under load without the test writing `z.token` itself (`client/client.go`, `Authorize`).
- Existing tests read and write `z.token` directly (`TestAuthorize`, `TestGetToken` "re-authorizes an expired token") and `TestGetToken` asserts both calls return the same pointer while the token is valid. These must keep passing unchanged.
- Existing tests pin the substrings `status 429`, `status 400`, `status 200` and `unable to get token`; their fake handlers send no body (`client/client_test.go`, `TestGetIntervalMeetings`, `TestGetCloudStorageReport`, `TestDeleteMeetingRecordings`).
- The current `GetIntervalMeetings` message is a raw string with a line break and tabs in the middle; the new one is a single line.
- `GetAllMeetingsWithRetry` logs the whole error on each retry, so an uncapped body would be logged up to 10 times.

## Definition of Done

- [x] A concurrent test reproduces the token bug before the fix: many goroutines released together on a fresh client cause more than one token request, and `-race` reports a data race — proof: the new test fails on the current code with `go test -race ./client/` and also with plain `go test ./client/`.
- [x] After the fix, concurrent callers on a fresh client cause exactly one token request — proof: the same test passes, asserting `tokenCalls == 1`.
- [x] Refreshing an expired token while other goroutines call the API is race-free — proof: a test with a short-lived fake token and concurrent API calls passes under `go test -race ./client/`.
- [x] A failed refresh keeps the previous token and returns an error — proof: test.
- [x] A non-200 from each of the three calls carries Zoom's body text — proof: tests where the fake answers with an error body assert the text appears in the error and `{}`-style reader output does not.
- [x] A 429 from `GetIntervalMeetings` no longer mentions authorization — proof: test asserts the error names the recordings call and does not contain "authorize".
- [x] A body larger than the cap is cut off — proof: test with an oversized body asserts the error length stays bounded.
- [x] Existing client tests pass without edits to their assertions — proof: `go test -race ./client/`.
- [x] Both backlog items are removed in the final commit — proof: `git rm docs/backlog/get-token-mutex-guards-nothing.md docs/backlog/zoom-error-messages-print-body-reader.md`.

## Work order

1. Write the concurrent token test and see it fail.
2. Fix the token handling.
3. Add the error-message tests, then the shared message helper.

## Wrap-up

- [x] branch is not behind its remote (`git fetch && git status`)
- [x] full test suite passes: `make test`, and `go test -race ./...`
- [x] linter passes: `make lint`
- [x] README.md / CLAUDE.md updated if behavior or patterns changed
- [x] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed && mv docs/plans/2026-10-01-zoom-client-token-race-and-error-messages.md docs/plans/completed/`)
- [x] single commit: all changes + plan move + backlog removals

## Post-Completion

- Consider adding `-race` to the `Makefile` `test` target once the parallel backlog work has landed; the whole suite passes under `-race` today.
