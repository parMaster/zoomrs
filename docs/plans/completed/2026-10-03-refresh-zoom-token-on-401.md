# Refresh the Zoom token on 401 and keep it out of the logs

**Goal:** When Zoom answers 401 while the stored token still looks fresh, get a new token and retry once; and never write the token to the logs.

**Kind of change:** bug fix

## Intent

The Zoom client only asks for a new token when its own expiry clock says so. If Zoom rejects
the token earlier (for example another process asked for a token with the same credentials,
GitHub issue #1), every call keeps failing with 401 until the clock runs out. Sync, cleanup
and downloads stall for that whole time.

Separately, the token reaches the logs in two ways: a debug line prints it right after it is
fetched, and download errors print the full download URL, which carries the token as a query
parameter.

After this change a 401 costs one extra token request and one retry, and the token value
does not appear in any log line or returned error.

```
before:  call ──401──▶ error ──▶ caller retries with the same dead token ... until expiry

after:   call ──401──▶ drop stored token ──▶ new token ──▶ same call again
                                                              ├─ ok   ──▶ result
                                                              └─ fail ──▶ error (no third try)
```

## Decisions

- **Retry exactly once per call** — a second 401 with a brand new token means the problem is
  not a stale token (wrong scopes, revoked app). More tries would only hammer Zoom. It also
  bounds the damage if two processes with the same credentials keep cancelling each other's
  tokens.
- **Only 401 triggers a refresh** — other statuses keep today's behaviour and error text.
- **Refresh is compare-and-swap** — the client replaces its stored token only if it is still
  the one the failed call used. The service's jobs share one client, so two jobs hitting 401
  at the same moment must end up with one token request, not two (and the second must not
  throw away the token the first just fetched).
- **The repository gets a way to report a rejected token** — the download happens outside
  the client, so the `Client` interface in `repo` grows one method that takes the rejected
  token and returns a current one:
  `RefreshToken(rejected *client.AccessToken) (*client.AccessToken, error)`.
  The three API calls inside the client use the same logic internally. Dropped alternative:
  moving the download into the client; bigger move than this fix needs.
- **Never log the token, instead of teaching the logger to hide it** — `lgr.Secret` only
  works for values known when the logger is built. Hiding a later token would mean rebuilding
  the std logger from inside the Zoom client on every refresh. Dropped for the coupling.
  Instead: the debug line that prints the token goes away, and download errors are stripped
  of the token before they are returned.
- **The token stays in the download URL query** — sending it as an `Authorization` header
  would be cleaner, but Zoom's download links redirect between hosts and Go drops that header
  on some cross-host redirects. It can't be checked without live credentials, so it is not
  part of this fix.

## Constraints / out of scope

- Error text for non-401 failures stays as it is; existing tests pin it.
- A failed token request during the retry returns an error that still names the original
  call, so logs show what was being attempted.
- No change to the retry loop in `GetAllMeetingsWithRetry` or to the download job's
  requeue of failed records.
- No change to logger setup in `cmd/service` or `cmd/cli`.
- Out of scope: the `Authorization` header for downloads; tokens of the web login
  (`webauth`).

## Traps

- `grab.Get` returns a non-nil error for any non-2xx answer (`StatusCodeError`), so a 401 on
  download arrives through the error branch, not through the later `StatusCode != 200` check.
  Detect it with the status on the response or the error type
  (`vendor/github.com/cavaliergopher/grab/v3/client.go`, `error.go`).
- Errors from `grab` / `net/http` for transport failures embed the request URL, query
  included. Stripping the token only from the repository's own format strings is not enough;
  the wrapped error text needs it too (`repo/repo.go`, `DownloadRecord`).
- `DownloadRecord` marks the record failed before returning an error. The retry must happen
  before that, so a download that succeeds on the second try never passes through `failed`.
- `GetIntervalMeetings` builds one request and reuses it across pages. A 401 can arrive on a
  later page; the retry has to carry the new token on that same page and keep the meetings
  already collected (`client/client.go`).
- `GetToken` and `authorize` rely on the caller holding the client's mutex; `authorize`
  stores a fresh token value on purpose, so pointer identity is a valid way to tell "still
  the rejected token" from "someone already refreshed" (`client/client.go`).
- `fakeClient` in `repo/repo_test.go` implements the `Client` interface and always returns
  the token `"tok"`; it needs the new method, and a way to hand out a different token after
  refresh so a test can tell the two apart.
- `TestStatusErrors` and `TestAuthorize` in `client/client_test.go` assert exact error text
  and token request counts for non-401 cases; `fakeZoom` counts token requests in
  `tokenCalls`, which is the natural proof for "one refresh, one retry".
- The token endpoint itself answers 401 for bad credentials (`TestAuthorize`). That is a
  failed authorization, not a stale token, and must not loop.

## Definition of Done

- [x] The bug is reproduced first — proof: a test where the fake Zoom rejects the first token
      with 401 and accepts the second fails before the fix (the call returns an error) and
      passes after.
- [x] Listing recordings, the storage report and deleting recordings each recover from one
      401 — proof: per call, a test showing success, exactly two token requests and the
      second attempt carrying the new token.
- [x] A 401 on a later page of recordings recovers without losing earlier pages — proof: a
      test with two pages, 401 on the second, all meetings returned.
- [x] A second 401 is returned as an error with no third attempt — proof: a test with a fake
      that always answers 401; the error names the call and status 401, the endpoint was hit
      twice, the token endpoint twice.
- [x] Two calls hitting 401 at the same time cause one token request — proof: a test running
      concurrent calls against a fake that rejects the first token, under `go test -race`,
      asserting the token request count.
- [x] A download rejected with 401 is retried once with a new token and succeeds without the
      record ever being marked failed — proof: a `DownloadRecord` test whose file server
      rejects the first token; the record ends `downloaded` and the store saw no `failed`
      update.
- [x] A download rejected twice marks the record failed and returns an error — proof: test.
- [x] No returned error or log line contains the token — proof: tests on the download
      failure paths (bad status, transport error, wrong size, wrong extension) assert the
      error text does not contain the token value; a test capturing log output around a
      token fetch asserts the same; `grep -n 'token = ' client/client.go` finds nothing.
- [x] Non-401 behaviour is unchanged — proof: the existing `client` and `repo` tests pass
      without edits to their assertions.

## Wrap-up

- [x] full test suite passes: `make test` (and `go test -race ./client/... ./repo/...`)
- [x] linter passes: `make lint`
- [x] README.md / CLAUDE.md updated if behavior or patterns changed
- [x] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed && mv docs/plans/2026-10-03-refresh-zoom-token-on-401.md docs/plans/completed/`)
- [x] single commit: all changes + plan move

## Post-Completion

- Close GitHub issue #1 once the fix is deployed.
- Optional live check: with the service running, request a token from the CLI and watch the
  service log for one refresh instead of repeated 401 errors.
