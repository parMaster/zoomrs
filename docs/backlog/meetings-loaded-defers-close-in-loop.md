---
worth: later
where: repo/repo.go:444
added: 2026-09-29
---
# requestMeetingsLoaded defers Body.Close inside the instances loop

Each instance's response body stays open until the function returns instead of after it's read. Harmless with the one or two instances configured today; worth fixing only if `commander.instances` grows or the loop moves into a long-lived caller. Also uses the package-level `http.Post`, with no timeout.
