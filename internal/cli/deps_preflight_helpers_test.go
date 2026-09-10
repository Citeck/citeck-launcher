package cli

import "github.com/citeck/citeck-launcher/internal/api"

// The CLI is a CONSUMER of the preflight: what it decodes is the daemon's
// already-rendered api.PreflightResult, never the migrator's own
// api.PreflightResult (whose Problems and Warnings are msg.Message and
// never reach the wire). These two builders stand in for the daemon.
//
// Both give Problems and Warnings a non-nil empty slice, because that is what
// the daemon's renderer produces and what every renderer downstream was
// written against — a fixture that used nil would let a nil-guard regression
// pass here and fail in the browser.

func newPreflightDto(from, to string) api.PreflightResult {
	return api.PreflightResult{From: from, To: to, Problems: []string{}, Warnings: []string{}}
}

func refusedPreflightDto(from, to string, problems ...string) api.PreflightResult {
	pre := newPreflightDto(from, to)
	pre.Problems = append(pre.Problems, problems...)
	return pre
}
