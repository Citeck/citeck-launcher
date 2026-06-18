// Package update implements desktop daemon auto-update: discovery of
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
// Invalid versions (e.g. "dev" / local-build markers) sort HIGHEST, so a dev
// build is always considered newer than any real release. In practice the only
// place an invalid version appears is the *current* running version (the b
// side); treating it as newest means a locally-built dev binary never offers
// to "update" itself down to a published release. Equal versions are NOT
// greater. Two invalid versions compare equal → not greater.
//
// Release versions discovered from GitHub are always valid semver, so the
// "a invalid" branch only fires in pathological inputs; it stays consistent
// (dev sorts above any real release) rather than silently flipping.
func Greater(a, b string) bool {
	ca, cb := canon(a), canon(b)
	aValid, bValid := semver.IsValid(ca), semver.IsValid(cb)
	switch {
	case !aValid && !bValid:
		return false // both dev → equal, no update
	case !aValid:
		return true // a is dev → newer than any real release b
	case !bValid:
		return false // b is dev → nothing is newer than a dev build
	default:
		return semver.Compare(ca, cb) > 0
	}
}
