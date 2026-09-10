package daemon

import (
	"errors"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// This file is the BOUNDARY: everything internal/deps/migrate and the
// dependency gates build as a msg.Message becomes a plain rendered string
// exactly here, in the language of the request that asked.
//
// It is deliberately the only place in the daemon that turns one of those into
// prose. A second renderer somewhere else is how "the dialog is translated and
// the CLI is not" comes back — the wire shape is unchanged (same JSON fields,
// same []string, never null), so nothing downstream can tell the difference
// and nothing downstream would fail if a path skipped the rendering.

// englishForLogs renders a Message for a reader who is NOT the operator of a
// request: an slog line, or a verdict persisted for the next launcher.
//
// It is a Translator and not a hand-written English string so the sentence
// still lives in exactly one place. Constructed once — the loaded locale maps
// are immutable and shared, so this is a pointer to read-only data.
var englishForLogs = i18n.NewTranslator("en")

// renderPreflight turns the pure result into the wire DTO. Every field is
// copied by hand rather than embedded, because the two types are allowed to
// diverge: the Go side may grow a fact the wire has no business carrying, and
// the wire side is a compatibility contract the CLI decodes.
//
// RenderAll gives Problems and Warnings a non-nil empty slice, which is the
// contract both consumers were built on — the web dialog maps over them
// without a guard, and a nil slice would marshal as null.
func renderPreflight(t *i18n.Translator, res migrate.PreflightResult) api.PreflightResult {
	return api.PreflightResult{
		OK:                   res.OK,
		Problems:             t.RenderAll(res.Problems),
		Warnings:             t.RenderAll(res.Warnings),
		From:                 res.From,
		To:                   res.To,
		DataSizeBytes:        res.DataSizeBytes,
		RequiredHostBytes:    res.RequiredHostBytes,
		RequiredVolumeBytes:  res.RequiredVolumeBytes,
		FreeHostBytes:        res.FreeHostBytes,
		FreeVolumeBytes:      res.FreeVolumeBytes,
		SharedFilesystem:     res.SharedFilesystem,
		RequiredTotalBytes:   res.RequiredTotalBytes,
		ExistingTargetVolume: res.ExistingTargetVolume,
		WasRunning:           res.WasRunning,
		SpaceChecked:         res.SpaceChecked,
	}
}

// renderProblems is the one-line form of a refusal that carries several
// sentences: the plan builder's, and the rollback route's, both of which have
// a single string to put them in.
//
// "; " is the separator both already used. It is punctuation between whole
// sentences rather than a phrase, so it needs no key of its own — and giving
// it one would invite a translator to change the SHAPE of a list the two
// renderers build identically.
func renderProblems(t *i18n.Translator, problems []msg.Message) string {
	return strings.Join(t.RenderAll(problems), "; ")
}

// renderPlanError is how a refused plan reaches the operator.
//
// migrate.Plan answers a refusal as a *migrate.PlanRefusedError carrying its
// sentences as data; anything else is a genuine failure whose text is already
// final (a docker error, an unreadable journal) and is passed through. The
// distinction matters because the two have different authors: one is the
// launcher talking to a person, the other is a library talking to a log.
func renderPlanError(t *i18n.Translator, err error) string {
	var refused *migrate.PlanRefusedError
	if errors.As(err, &refused) {
		return renderProblems(t, refused.Problems)
	}
	return err.Error()
}

// renderRollbackOffer fills in the offer's rendered Problem from the Message
// beside it, and hands the same pointer back so a caller can keep using it
// inline.
//
// The Message is NOT cleared afterwards: it never reaches the wire (json:"-"),
// and the edit gate reads its Key to tell "the retained volume is gone" from
// every other unavailable offer — a comparison that has to survive the offer
// being rendered into Russian.
func renderRollbackOffer(t *i18n.Translator, offer *api.DependencyRollbackDto) *api.DependencyRollbackDto {
	if offer == nil {
		return nil
	}
	if !offer.ProblemMsg.Empty() {
		offer.Problem = t.Render(offer.ProblemMsg)
	}
	return offer
}

// renderMigrationDto renders the running migration's step message for one
// reader. The DTO is published once, daemon-globally, and read back by every
// client in its own language — so the copy the caller got (currentDepsMigration
// hands out a copy by design) is where the language is applied.
func renderMigrationDto(t *i18n.Translator, dto *api.DependencyMigrationDto) *api.DependencyMigrationDto {
	if dto == nil {
		return nil
	}
	if !dto.MessageMsg.Empty() {
		dto.Message = t.Render(dto.MessageMsg)
	}
	return dto
}

// resultDto renders the last migration verdict for the wire.
//
// Error prefers the STRUCTURED verdict and falls back to the English string,
// which is what an older launcher's persisted result carries and what a raw
// step failure — a docker error, a psql stderr — legitimately has instead of a
// sentence. The fallback is not a courtesy: MigrationResult.Error is the field
// OK() is decided on and the field the state file has always held, so it is
// never absent when there is a failure to report.
func resultDto(t *i18n.Translator, r *deps.MigrationResult) *api.DependencyMigrationResultDto {
	if r == nil {
		return nil
	}
	text := r.Error
	if !r.ErrorMsg.Empty() {
		text = t.Render(r.ErrorMsg)
	}
	return &api.DependencyMigrationResultDto{
		ID: string(r.ID), From: r.From, To: r.To, FinishedAt: r.FinishedAt.UnixMilli(),
		Success: r.OK(), Error: text, OldVolume: r.OldVolume, Kind: r.Kind,
	}
}
