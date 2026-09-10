package migrate

import (
	"strings"

	"github.com/citeck/citeck-launcher/internal/msg"
)

// This file holds the two shapes an operator-facing sentence takes on its way
// OUT of this package, beside the preflight's Problems/Warnings: a refusal
// that has to travel as a Go error, and the running commentary of a step.
//
// The rule the whole package follows: a sentence the OPERATOR reads is a
// msg.Message (a locale key plus its arguments, rendered at the HTTP boundary
// where the reader is known), while a sentence WE read — an slog line, the
// persisted MigrationResult.Error, a wrapped step failure — stays English.
// Those two are different audiences, and trying to serve both with one string
// is what made every sentence here English in all eight languages.

// PlanRefusedError is a plan that was refused before it was built, carrying
// its reasons as DATA so the route can render them in the caller's language.
//
// The four refusals it covers are the ones a user can actually hit — a
// preflight that failed, a target volume they have not agreed to replace, and
// (defensively) an unregistered dependency — and they were the last sentences
// on the migrate route that reached the operator as English prose baked into a
// Go error.
//
// Error() is deliberately NOT the operator's sentence. It is the LOG's, and it
// names the keys rather than rendering them: this package has no locale, and a
// log line naming "deps.msg.space.hostShort" tells whoever reads it exactly
// which sentence fired and where to find its text, which a half-rendered
// English guess would not. Callers that have a reader render Problems
// themselves (see the daemon's renderPlanError).
type PlanRefusedError struct {
	Problems []msg.Message
}

func (e *PlanRefusedError) Error() string {
	keys := make([]string, 0, len(e.Problems))
	for _, m := range e.Problems {
		keys = append(keys, m.Key)
	}
	return "preflight failed: " + strings.Join(keys, "; ")
}

// refusePlan builds a PlanRefusedError from one or more messages. Every plan
// refusal goes through it so none of them can quietly go back to fmt.Errorf.
func refusePlan(problems ...msg.Message) error {
	return &PlanRefusedError{Problems: problems}
}

// existingVolumeRefusal is the shared "you have not agreed to delete this"
// refusal of both plan builders. It is one function because the PostgreSQL
// plan and the copy upgrade were already spelling it identically, and two
// spellings of one refusal is two translations of one sentence.
func existingVolumeRefusal(volume string) msg.Message {
	return msg.New("deps.msg.plan.existingVolume", "volume", volume)
}

// progressWaiting is the "waiting for X in container Y" line every readiness
// wait prints. `what` is a product name (PostgreSQL, RabbitMQ, ZooKeeper) and
// stays untranslated on purpose — it is a proper noun, not a word.
func progressWaiting(what, container string) msg.Message {
	return msg.New("deps.msg.progress.waiting", "what", what, "container", container)
}

// passthrough carries a string that is ALREADY final through the message
// pipeline: a Go error, a vendor tool's stderr, the joined notes of an
// inventory comparison. Its locale value is "{text}" in all eight files, so
// rendering it is the identity.
//
// It exists so that "this text has no translation and here is why" is a
// visible decision at the call site rather than a string that silently escapes
// the pipeline. Do NOT use it for a sentence the launcher itself writes — that
// is a missing key, not a passthrough.
func passthrough(text string) msg.Message {
	return msg.New("deps.msg.passthrough", "text", text)
}
