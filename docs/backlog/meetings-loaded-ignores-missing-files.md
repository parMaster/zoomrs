---
worth: yes
where: cmd/service/api.go:417
added: 2026-09-29
---
# meetingsLoaded answers "ok" for a downloaded record whose file is gone

`meetingsLoadedHandler` compares file sizes only when `os.Stat` succeeds, so a record marked `downloaded` whose file was deleted from disk still counts as loaded. `CleanupJob` in the CLI trusts that "ok" and deletes the recordings from Zoom, which may be the only copy left. Pinned by `TestMeetingsLoadedHandler/ok_even_when_a_downloaded_file_is_missing`; the fix flips that assertion to `pending`.
