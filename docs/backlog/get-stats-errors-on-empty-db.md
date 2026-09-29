---
worth: yes
where: repo/repo.go:576
added: 2026-09-29
---
# GetStats treats "no downloaded records" as an error

`GetStats` checks `recs == nil` instead of `err`. sqlite's `GetRecordsByStatus` returns a nil slice for an empty result, so with nothing downloaded `GetStats` returns an error wrapping a nil error, and `/stats` answers 500 instead of an empty map. It also ignores `err` when rows came back. Pinned by `TestGetStats/no_downloaded_records_is_an_error` and `TestStatsHandler_NoDownloadsIs500`.
