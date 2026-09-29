---
worth: yes
where: client/client.go:173
added: 2026-09-29
---
# Zoom client error messages print the body reader and mislabel failures

Non-200 errors in `GetIntervalMeetings`, `GetCloudStorageReport` and `DeleteMeetingRecordings` format `resp.Body` (an `io.ReadCloser`) with `%s`, so logs show `message: {}` instead of Zoom's error text. `GetIntervalMeetings` also says "unable to authorize with account id..." for any non-200, which sends debugging toward credentials when the real cause is e.g. a 429 rate limit. Read the body (capped) into the message and say what failed.
