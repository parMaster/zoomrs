---
worth: yes
where: client/client.go:112
added: 2026-09-29
---
# GetToken's mutex is created per call and guards nothing

`GetToken` declares `var mx sync.Mutex` inside the function, so each caller locks its own mutex. Concurrent callers (the service's sync and download jobs share one client) race on `z.token` and can authorize twice. The mutex belongs on `ZoomClient`. There is no test for it: a concurrent test fails under `-race` until the fix lands, so write that test together with the fix.
