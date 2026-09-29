---
worth: yes
where: cmd/service/main.go:99
added: 2026-09-29
---
# HTTP server is never shut down on cancel

`startServer` calls `Shutdown` only after `ListenAndServe` returns, but `ListenAndServe` blocks until the server is shut down, so a SIGTERM never drains in-flight requests; the process just exits when `Run` returns. `Shutdown` needs to run from a goroutine watching `ctx.Done()`. `TestRun_ServesUntilCanceled` passes today only because `Run` doesn't wait for `startServer`.
