// Package update implements desktop daemon auto-update (Spec 2b): discovery of
// the GitHub `latest` release, changelog fetch, payload download/verify/stage,
// and the on-disk manifest the wrapper uses to swap and roll back the daemon
// binary. It is pure Go (no Wails/daemon imports) so it unit-tests on the host.
package update

import (
	"strings"

	"golang.org/x/mod/semver"
)

// canon normalizes a version for semver comparison: ensures a leading "v".
// An empty string stays empty (semver.IsValid will reject it).
func canon(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if v[0] != 'v' {
		return "v" + v
	}
	return v
}

// IsValidVersion reports whether v is a syntactically valid release version
// (semver, with or without a leading "v"). It is used to reject path-unsafe
// version strings (e.g. "..", values containing separators) before they are
// joined into filesystem paths — semver rejects anything that is not a clean
// MAJOR.MINOR.PATCH[-pre][+meta] token.
func IsValidVersion(v string) bool {
	return semver.IsValid(canon(v))
}

// Greater reports whether release a is strictly newer than release b.
// Invalid versions (e.g. "dev" builds) sort lowest, so a dev build is always
// considered older than any real release — updates are offered and stageable
// during local testing. Equal versions are NOT greater.
func Greater(a, b string) bool {
	ca, cb := canon(a), canon(b)
	if !semver.IsValid(ca) {
		ca = "v0.0.0"
	}
	if !semver.IsValid(cb) {
		cb = "v0.0.0"
	}
	return semver.Compare(ca, cb) > 0
}
