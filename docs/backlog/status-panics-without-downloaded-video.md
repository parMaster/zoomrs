---
worth: yes
where: cmd/service/api.go:149
added: 2026-09-29
---
# /status panics when no meeting has a downloaded video

`statusHandler` takes `meetingsLoaded[0]` from `ListMeetings` without checking the length. On a fresh install, or while only audio is downloaded, the handler panics; `net/http` recovers it and the client sees a dropped connection instead of a status. Pinned by `TestStatusHandler/panics_when_nothing_is_downloaded_yet`.
