# Service lifecycle and error wrapping

**Goal:** The service stops its HTTP server cleanly on cancel, and every error printed or wrapped with `%e` uses `%w` / `%v` instead.

**Kind of change:** bug fix (two backlog items: `server-never-shuts-down`, `errors-formatted-with-percent-e`)

## Intent

Two things are wrong today.

**Shutdown.** `startServer` only calls `Shutdown` after `ListenAndServe` returns, but `ListenAndServe` blocks until someone calls `Shutdown`. So `Shutdown` is never reached. On SIGTERM `Run` returns, the process exits, and any request still being answered is cut off. The listener is never closed by our code.

**Error verbs.** `%e` is the float verb. Used on an error it prints `&{%!e(string=...)}` and, inside `fmt.Errorf`, it does not wrap, so `errors.Is` / `errors.As` can't see the cause. It is used in 7 places across both commands and the config package.

```
before                                   after
------                                   -----
Run                                      Run
 ├─ go startServer ──► ListenAndServe     ├─ go startServer ──► ListenAndServe
 │                      (blocks forever)  │        ▲
 │                      Shutdown: never   │        │ ctx done ─► Shutdown(fresh ctx, timeout)
 └─ <-ctx.Done() ─► return                │        │             waits for open requests
    (listener still open,                 └─ <-ctx.Done() ─► wait for startServer ─► return
     requests cut off at exit)               (listener closed, requests finished)
```

## Decisions

- **`Shutdown` runs when `ctx` is done, while `ListenAndServe` is still serving** — this is the fix the backlog item names. Whether the watcher is a goroutine and serving stays on the caller, or the other way round, is the implementer's choice.
- **`Shutdown` gets its own context with a timeout, not the server `ctx`** — the server `ctx` is already cancelled at that point, and `Shutdown` with a cancelled context closes the listener and returns at once without waiting for open requests.
- **Drain timeout is 5 seconds** — `docker-compose.yml` sets no `stop_grace_period`, so Docker kills the container 10 seconds after SIGTERM; the drain has to fit inside that with room to spare. Dropped: 30s to match `WriteTimeout` (Docker would kill it first).
- **`Run` waits for `startServer` to return before it returns** — otherwise `main` exits while the drain is still running and the fix does nothing. `Run` does not wait for `SyncJob` / `DownloadJob` (out of scope).
- **If the drain times out, the serving function calls `Close()` before it returns** — `Shutdown` that times out leaves the stuck connections open. In production the process exits right after, but in tests the server would live on into later tests in the same package.
- **The serving part takes an `http.Handler`** — split `startServer` so the listen/serve/shutdown logic can be given a handler, with `startServer` passing `s.router(ctx)`. A test then proves the drain with its own slow handler. Dropped: proving the drain through a real route (none is slow, and all of them read the store through the cancelled `ctx`, see Traps).
- **A failed listen keeps today's behavior** — log the error, keep the jobs running, return once `ctx` is done. `TestStartServer_BadAddressReturnsAfterCancel` pins this and must keep passing.
- **`%w` in `fmt.Errorf`, `%v` in log lines** — mechanical, as the backlog item says. No message text changes beyond the verb.
- **The `LoadStorage` tests assert the cause with `errors.Is(err, context.Canceled)`** — call `LoadStorage` with an already cancelled context; `sqlite.NewStorage` returns the ping error unwrapped. Dropped: asserting on the "can't open file" case with `errors.Is`, because the driver's error type has no `Is` method (see Traps). That case stays, with its full message checked instead of only the prefix.

## Constraints / out of scope

- `log.Fatalf` in `NewServer` and `Run` stays; only the verb changes.
- Handlers reading the store through the server `ctx` is not fixed here; it changes every handler in `cmd/service/api.go` and is its own change.
- `Run` waiting for `SyncJob` / `DownloadJob` to finish is not added here; neither backlog item asks for it.
- No change to how `main` handles signals.

## Traps

- Handlers are built with the server `ctx` and pass it to every store call, and the SQLite store closes the database when that same `ctx` is done. So after cancel, a request that still needs the store fails even though the server waits for it. The drain only helps requests that are past their store calls (for example, streaming a response). The drain test must use its own handler, not a real route. (`cmd/service/api.go`, `router`; `storage/sqlite/storage.go`, `NewStorage`)
- `TestRun_ServesUntilCanceled` only checks that `Run` returns after cancel. It passes today because `Run` never waits for the server, so it proves nothing about shutdown. After `Run` returns today, the port still accepts connections. (`cmd/service/handlers_test.go`)
- `TestStartServer_BadAddressReturnsAfterCancel` calls `startServer` directly with a cancelled `ctx` and a bad address and expects it to return. A design where the shutdown watcher and the serve call wait on each other can hang here. (`cmd/service/handlers_test.go`)
- "Connection refused after `Run` returns" does not prove that `Run` waits. `Shutdown` closes the listener as its first step, so a `Run` that still returns on `<-ctx.Done()` passes that check almost every time by luck of timing. The wait needs its own test, through `Run`, with a request still open at cancel time.
- `Close()` drops connections but does not stop a handler that is still running. The never-returning handler in the timeout test must be released in test cleanup, or its goroutine leaks.
- `ListenAndServe` returns `http.ErrServerClosed` as soon as `Shutdown` starts, not when it finishes. `startServer` must return only after `Shutdown` has returned, or `Run` stops waiting too early.
- `sqlite3.Error` is a plain struct with no `Is` method, so `errors.Is(err, sqlite3.ErrCantOpen)` is false even after the `%w` fix. Only `errors.As` into `sqlite3.Error` and comparing `.Code` works for driver errors. (`vendor/github.com/mattn/go-sqlite3/error.go`)
- `LoadStorage` exists twice with the same body, in `cmd/service/main.go` and `cmd/cli/main.go`, each with its own `TestLoadStorage`. Both need the fix and the tighter test. Both tests carry a comment saying only the prefix is reliable because of `%e`; that comment goes away.
- `make test` runs without `-race`. The lifecycle tests start goroutines, so run them with `-race` too.

## Definition of Done

- [x] The shutdown bug is reproduced first — proof: a tightened `TestRun_ServesUntilCanceled` asserts that once `Run` has returned, a new connection to the listen address is refused; it fails on the current code and passes after the fix.
- [x] A request in flight at cancel time is finished, not cut off — proof: a test serves a handler that blocks until released, cancels `ctx` mid-request, releases the handler, and the client gets the full 200 response; the serving function returns only after that. Fails on the current code.
- [x] `Run` does not return while a request is still open — proof: a test through `Run` (not the split-out serving function) has a request open at cancel time and asserts `Run` has not returned until that request ends. Fails if `Run` returns on `<-ctx.Done()` without waiting. How the request is held open is the implementer's choice: a handler the test can swap in, or a raw TCP connection that has sent half a request line (the server waits for it until `ReadHeaderTimeout`, 1s).
- [x] A request that outlasts the drain timeout does not hang shutdown — proof: a test with a handler that blocks past the timeout shows the serving function comes back after the timeout, and a new connection is refused afterwards (the timeout must be settable from the test so it doesn't take 5 seconds; the handler is released in test cleanup).
- [x] A failed listen still returns after cancel — proof: `TestStartServer_BadAddressReturnsAfterCancel` passes unchanged.
- [x] `LoadStorage` wraps its cause in both commands — proof: `TestLoadStorage` in `cmd/service` and in `cmd/cli` each assert `errors.Is(err, context.Canceled)` for a cancelled context; both fail before the `%w` fix. The missing-directory case checks the whole message (`failed to init SQLite storage: unable to open database file`…), not just the prefix.
- [x] No `%e` is left in the code — proof: `grep -rn '%e' --include='*.go' --exclude-dir=vendor .` prints nothing (this also covers the two stale test comments).
- [x] Backlog items are removed — proof: `git rm docs/backlog/server-never-shuts-down.md docs/backlog/errors-formatted-with-percent-e.md` is part of the final commit.

## Wrap-up

- [x] `git fetch && git status`; if behind, `git pull --rebase`
- [x] full test suite passes: `make test`, plus `go test -race ./cmd/... ./config/...`
- [x] linter passes: `make lint`
- [x] README.md updated only if it describes shutdown behavior (it likely doesn't)
- [x] `docs/plans/wbs-backlog-cleanup.md`: set iteration 3 status to done and add a progress-log line; also set iteration 2 to done with its own log line (commit `8254042`, plan already in `completed/`) — the file is untracked, so that commit did not carry the update
- [x] move this plan to `docs/plans/completed/` (`mv docs/plans/2026-10-01-service-lifecycle-and-error-wrapping.md docs/plans/completed/`)
- [x] single commit: all changes + backlog removals + plan move
