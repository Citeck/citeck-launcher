package migrate

import (
	"errors"
	"strings"

	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// renderEN turns this package's messages into the sentences an English reader
// gets, so the tests can go on asserting on the wording an operator sees.
//
// It renders through the REAL locale files rather than through a fixture, and
// that is the point: a key a builder invents but never adds to en.json renders
// as the raw key, so every assertion below doubles as a "this sentence exists"
// check. (The daemon's TestLocaleCompleteness then pins the other seven.)
//
// Importing internal/i18n here is a test-only dependency and must stay one:
// the production side of this package is deliberately locale-free, which is
// why the sentences leave it as msg.Message in the first place.
func renderEN(ms []msg.Message) []string {
	return i18n.NewTranslator("en").RenderAll(ms)
}

// joinEN is the shape most assertions want: every problem (or warning) as one
// searchable block.
func joinEN(ms []msg.Message) string {
	return strings.Join(renderEN(ms), "\n")
}

// planProblemsEN is the same for a refusal that traveled as an error: the
// operator's sentences, in English. It also asserts the SHAPE — a plan
// refusal must be a *PlanRefusedError, because that is what lets the route
// render it in the caller's language instead of quoting Error(), which names
// keys for the log.
func planProblemsEN(tb testingTB, err error) string {
	tb.Helper()
	var refused *PlanRefusedError
	if !errors.As(err, &refused) {
		tb.Fatalf("expected a *PlanRefusedError, got %T: %v", err, err)
		return ""
	}
	return joinEN(refused.Problems)
}

// testingTB is the sliver of *testing.T planProblemsEN needs.
type testingTB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// oneEN renders a single message, for the tests that assert on a builder's
// answer directly rather than on a preflight's list.
func oneEN(m msg.Message) string { return i18n.NewTranslator("en").Render(m) }
