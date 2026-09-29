---
worth: yes
added: 2026-09-29
---
# Errors formatted with %e instead of %w / %v

`LoadStorage` in `cmd/service/main.go` and `cmd/cli/main.go` wraps the sqlite error with `%e`, which prints `&{%!e(string=...)}` and breaks `errors.Is`. The same verb is used in log lines in `config/config.go` (`NewConfig`), `cmd/service/main.go` (`NewServer`, `Run`) and `cmd/cli/ui.go`. Mechanical fix: `%w` in `fmt.Errorf`, `%v` in logs. The `LoadStorage` tests in both commands only check the message prefix for this reason.
