# Migrate to go-pkgz/auth v2

**Goal:** Move from `github.com/go-pkgz/auth` v1 to `github.com/go-pkgz/auth/v2` so the build no longer pulls in `github.com/golang-jwt/jwt` v3.

**Kind of change:** migration

## Intent

govulncheck reports GO-2025-3553 in `golang-jwt/jwt` v3.2.2+incompatible, which has no fixed version, and our code reaches it through `go-pkgz/auth/token`. Even the latest auth v1 (v1.27.1) still depends on jwt v3. auth v2 (v2.3.1) uses `golang-jwt/jwt/v5`, so switching to it removes the vulnerable module. Login with Google and the managers check must keep working exactly as they do today.

## Decisions

- **auth v2.3.1, not a fork or replace directive** — v2 is the maintained line, and it is a mostly path-only change for the parts we use. Keeping v1 and adding a `replace` for jwt isn't possible: jwt v3's API differs from v5.
- **Tests move to `golang-jwt/jwt/v5` directly** — they build `token.Claims` by hand, and v2 embeds `jwt.RegisteredClaims` (v5) instead of `jwt.StandardClaims` (v3).
- **Accept a one-time re-login** — see the old-cookies trap. Cookies last 1 day, so a compatibility shim isn't worth it.

## Constraints / out of scope

- No change to auth behavior: same Google provider, scopes, token/cookie durations, issuer, managers validator, XSRF setting, avatar store.
- `mongo-driver` stays at v1 (it is deprecated in favor of v2). auth v2 still requires mongo-driver v1, so moving it is not up to us.
- Don't touch the dep bumps already committed on this branch (`ca9a7c3`).

## Traps

- **v2 `Token()` crashes on claims with no audience** — it calls `SecretReader.Get(claims.Audience[0])` without checking the length when no `AudienceReader` is set (`token/jwt.go`, `Service.Token`, in auth v2). The hand-built claims in `webauth/webauth_test.go` and `cmd/service/router_test.go` (`authHeader`) set no audience, so they must set exactly one entry (e.g. `jwt.ClaimStrings{""}` or the domain).
- **v2 rejects tokens whose audience isn't exactly one entry** — `Service.Get` returns "aud is not of size 1" when the claims carry a user (`token/jwt.go`). The real login flow always sets one entry, `[]string{site-or-aud-param}`, which is `[""]` for us because `web/index.html` and `web/auth.html` pass only `from=` (`provider/oauth2.go`, `loginHandler`). So production login is fine; only hand-built tokens need care.
- **Old v1 cookies stop working** — v1 stores `aud` as a string with `omitempty`, so our tokens (empty site) carry no `aud` at all. After the upgrade, logged-in users hit the size-1 check and must log in again. This happens once.
- **Time fields change type** — `ExpiresAt` and the other time fields go from `int64` Unix seconds to `*jwt.NumericDate` (`jwt.NewNumericDate(t)`).
- **Same type names in v2** — `token.SecretFunc`, `ValidatorFunc`, `ClaimsUpdFunc`, `HashID`, `GetUserInfo`, `provider.CustomHandlerOpt` (Endpoint/InfoURL/MapUserFn/Scopes) and `auth.Client` all keep their names and shapes, so `webauth/webauth.go` should need import changes only. `Opts.Logger` is now `auth/v2/logger.L`, which `lgr.Std` still satisfies through `Logf`.
- **The vendor folder is gitignored** — run `go mod tidy && go mod vendor` after changing imports, or the build fails with inconsistent-vendoring errors. Only `go.mod`/`go.sum` get committed.

## Definition of Done

- [x] Every import of `github.com/go-pkgz/auth/...` uses `/v2`, and `go.mod` no longer lists `github.com/go-pkgz/auth` v1 or `github.com/golang-jwt/jwt` — proof: `grep -rnE 'go-pkgz/auth(/(avatar|provider|token))?"|golang-jwt/jwt"' --include='*.go' . | grep -v '^./vendor'` finds nothing, and `go mod why -m github.com/golang-jwt/jwt` prints "main module does not need module"
- [x] No reachable vulnerabilities remain — proof: `govulncheck ./...` prints "No vulnerabilities found" (module-only results with no fix, like mongo-driver advisories, get noted in the PR if they show up)
- [x] ➕ govulncheck still lists GO-2026-5932 at module level only: `golang.org/x/crypto/openpgp` is deprecated with no fix, and nothing here imports it
- [x] The managers check still allows a manager and blocks anyone else — proof: the existing `webauth` tests and the router tests that use `authHeader` pass with v5 claims that carry one audience entry
- [x] Google user mapping is unchanged — proof: the existing `mapGoogleUser` test passes with no assertion changes

## Wrap-up

- [x] full test suite passes: `make test`
- [x] linter passes: `make lint`
- [x] README.md / CLAUDE.md updated if behavior or patterns changed
- [x] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed && mv docs/plans/2026-10-23-migrate-to-go-pkgz-auth-v2.md docs/plans/completed/`)
- [x] single commit: all changes + plan move

## Post-Completion

- Manual smoke test after deploy: log in with Google as a manager, confirm records load; confirm a non-manager is refused.
- Expect logged-in users to be logged out once after deploy (old cookies).
