package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// The localization contract of the dependency surface, from the four angles
// that can each break on their own:
//
//  1. the sentence really changes with the request's language;
//  2. an error BODY follows the same header as a JSON body;
//  3. the SSE stream, which cannot carry a header, follows ?locale=;
//  4. an unmarked request follows daemon.yml — which is server mode, where
//     there is no client to change.
//
// They all pin the same seam from different sides on purpose: the wire shape
// is unchanged by this feature, so nothing downstream fails if a route quietly
// stops rendering. Only a test can notice.

// depsGetLocale is depsGet with a stated language.
func depsGetLocale(mux *http.ServeMux, path, locale string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	if locale != "" {
		req.Header.Set(api.LocaleHeader, locale)
	}
	mux.ServeHTTP(rec, req)
	return rec
}

// translatedProbeKey is an EXISTING, genuinely translated key, used as the
// preflight's problem in the two tests that have to prove a language really
// reached a route.
//
// The deps.msg.* sentences this change adds carry English in all eight files
// for now — a later pass translates them — so asserting on one of them would
// pass whether or not the route rendered in the right language. This key
// differs per locale today, so the assertion fails the moment the rendering
// stops following the request. (It names a MIGRATION STEP rather than a
// problem, which no production path would put here; that is fine — the route
// renders whatever the migrator hands it, and what is under test is the
// rendering, not the wording.)
const translatedProbeKey = "deps.step.dump"

// localeProbeDaemon is a preflight route wired to a migrator whose single
// problem is translatedProbeKey.
func localeProbeDaemon(t *testing.T) (*Daemon, *http.ServeMux) {
	t.Helper()
	d, mux, _ := newDepsRoutesDaemon(t)
	d.activeNs.dockerClient = &docker.Client{}
	d.depsEnvFn = func(activeNamespace) migrate.Env { return migratetest.New() }
	pre := migrate.NewPreflightResult("postgres:17.5", "postgres:18")
	pre.Problems = append(pre.Problems, msg.New(translatedProbeKey))
	d.depsMigratorFn = func(deps.ID) migrate.Migrator { return fakeMigrator{pre: pre} }
	return d, mux
}

// Does the language actually reach the sentence? Two requests to one daemon,
// two headers, two languages — which is also the case the whole design exists
// for: a desktop UI and a CLI talking to the same process at the same moment.
func TestPreflightProblemsFollowTheRequestLocale(t *testing.T) {
	_, mux := localeProbeDaemon(t)

	en := decodePreflight(t, depsGetLocale(mux, api.DependencyPreflightPath("postgres"), "en"))
	ru := decodePreflight(t, depsGetLocale(mux, api.DependencyPreflightPath("postgres"), "ru"))
	require.Len(t, en.Problems, 1)
	require.Len(t, ru.Problems, 1)

	assert.Equal(t, i18n.NewTranslator("en").T(translatedProbeKey), en.Problems[0])
	assert.Equal(t, i18n.NewTranslator("ru").T(translatedProbeKey), ru.Problems[0])
	assert.NotEqual(t, en.Problems[0], ru.Problems[0], "the same refusal in two languages")

	// A region tag and an unknown language are both answered, never refused.
	assert.Equal(t, ru.Problems[0],
		decodePreflight(t, depsGetLocale(mux, api.DependencyPreflightPath("postgres"), "ru-RU")).Problems[0])
	assert.Equal(t, en.Problems[0],
		decodePreflight(t, depsGetLocale(mux, api.DependencyPreflightPath("postgres"), "klingon")).Problems[0])
}

// The rendered arrays must never marshal as null: the web dialog maps over
// both without a guard, and the happy path is exactly the one with nothing in
// them. This is the wire half of the migrator's TestPreflightKeepsEmptyListsNonNil.
func TestRenderedPreflightMarshalsEmptyListsNotNull(t *testing.T) {
	clean := migrate.NewPreflightResult("postgres:17.5", "postgres:18")
	clean.OK = true
	raw, err := json.Marshal(renderPreflight(i18n.NewTranslator("ru"), clean))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"problems":[]`)
	assert.Contains(t, string(raw), `"warnings":[]`)

	// And a NIL pair — which no builder should produce, but which a hand-built
	// literal can — still renders as arrays rather than as null.
	raw, err = json.Marshal(renderPreflight(i18n.NewTranslator("en"),
		migrate.PreflightResult{From: "a", To: "b"}))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"problems":[]`)
	assert.Contains(t, string(raw), `"warnings":[]`)
}

// An error BODY is a different code path from a JSON body — writeErrorCode,
// not writeJSON — and the edit gate is the one that builds TWO sentences and
// joins them. Both halves have to be RENDERED, in the language the request
// asked for, and joined by a space.
//
// A caveat worth stating rather than hiding: while the deps.msg.* values are
// English in all eight files, this test cannot distinguish "rendered in ru"
// from "rendered in en" — the two are the same string. What it does pin, and
// what a mutation does break, is that both sentences are there, that neither
// arrives as a raw locale key, and that the join is a space and not nothing.
// The LANGUAGE half of the same seam is pinned on a route whose key really is
// translated: TestPreflightProblemsFollowTheRequestLocale.
func TestEditGateRefusalFollowsTheRequestLocale(t *testing.T) {
	mux, _, _ := newBackwardsEditGateDaemon(t)

	body := func(locale string) string {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/apps/postgres/config",
			strings.NewReader("name: postgres\nimage: postgres:17.5\n"))
		req.Header.Set("Content-Type", "application/yaml")
		req.Header.Set(api.LocaleHeader, locale)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		return rec.Body.String()
	}

	tEn, tRu := i18n.NewTranslator("en"), i18n.NewTranslator("ru")
	enBody := body("en")
	// Both sentences, in the same body, in the same language.
	assert.Contains(t, enBody, tEn.T("deps.msg.edit.backwardsBreaking",
		"app", "postgres", "pinned", "postgres:18.6", "wanted", "postgres:17.5"))
	assert.Contains(t, enBody, tEn.T("deps.msg.edit.wayBackRollback", "id", "postgres", "volume", "postgres2"))

	ruBody := body("ru")
	assert.Contains(t, ruBody, tRu.T("deps.msg.edit.backwardsBreaking",
		"app", "postgres", "pinned", "postgres:18.6", "wanted", "postgres:17.5"))
	assert.Contains(t, ruBody, tRu.T("deps.msg.edit.wayBackRollback", "id", "postgres", "volume", "postgres2"))

	// Rendered, never a raw key: a route that forgot to render would put
	// "deps.msg.edit.backwardsBreaking" in front of the operator.
	assert.NotContains(t, enBody, "deps.msg.")
	// And joined by a SPACE. The two are separate sentences precisely so a
	// translator can move each one; running them together would undo that.
	assert.Contains(t, enBody, "moves no data at all. The way back is")
}

// The backwards refusal is TWO sentences and not one, because the second — the
// way back — is a fact the caller establishes and its content is unknown to the
// first. Glued together with an em dash it was one English sentence with a
// clause of unknown shape in the middle, which is the one form no translator
// can rearrange.
func TestABackwardsRefusalIsTwoSentences(t *testing.T) {
	desc, ok := deps.Lookup(deps.Postgres)
	require.True(t, ok)
	r := dependencyEditRefusal{
		desc: desc, app: "postgres", pinned: "postgres:18.6", wanted: "postgres:17.5",
		reason: editReasonBackwards, breaking: true,
	}
	got := r.message(&api.DependencyRollbackDto{Available: true, Volume: "postgres2"})
	require.Len(t, got, 2)
	assert.Equal(t, "deps.msg.edit.backwardsBreaking", got[0].Key)
	assert.Equal(t, "deps.msg.edit.wayBackRollback", got[1].Key)

	// The FLOOR and the FORWARD refusals name no way back at all — offering a
	// rollback onto an unsupported version would offer the thing just refused.
	r.reason, r.breaking = editReasonBelowFloor, false
	assert.Len(t, r.message(nil), 1)
	r.reason = editReasonBreakingForward
	assert.Len(t, r.message(nil), 1)
}

// EventSource cannot set a header, which is the whole reason
// api.LocaleQueryParam exists. The migration's per-step messages ride that
// stream, so if the param did not reach them the dialog's progress screen
// would be the one surface still in English.
func TestSSEProgressMessagesFollowTheQueryParam(t *testing.T) {
	progress := migrate.PostgresStepIDs()[0]
	evt := depsEvent(api.EventDepsMigrationProgress, "ns1", deps.Postgres, progress, 1, 10, 0,
		msg.New("deps.msg.progress.copying", "size", "512.0 MiB"))

	frame := func(locale string) string {
		var sb strings.Builder
		writeSSEEvent(&sb, i18n.NewTranslator(locale), evt)
		return sb.String()
	}
	render := func(locale string) string {
		raw := frame(locale)
		var payload api.EventDto
		_, data, found := strings.Cut(raw, "data: ")
		require.True(t, found)
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(data)), &payload))
		// The MESSAGE must not ride along on the wire: `after` is the whole
		// contract every client was written against, and a second field
		// carrying the same sentence unrendered is one both would have to
		// learn to ignore.
		assert.NotContains(t, raw, "afterMsg")
		assert.NotContains(t, raw, "deps.msg.")
		return payload.After
	}

	assert.Equal(t, i18n.NewTranslator("en").T("deps.msg.progress.copying", "size", "512.0 MiB"), render("en"))
	assert.Equal(t, i18n.NewTranslator("ru").T("deps.msg.progress.copying", "size", "512.0 MiB"), render("ru"))

	// And the query param really is what translatorFor reads off an SSE URL:
	// the header route is not available to EventSource at all.
	d := &Daemon{}
	req := httptest.NewRequest(http.MethodGet, api.Events+"?"+api.LocaleQueryParam+"=ru", http.NoBody)
	assert.Equal(t, "ru", d.translatorFor(req).Locale())

	// The published event itself must NOT carry a rendered sentence: it is
	// broadcast once and read by every subscriber in its own language.
	assert.Empty(t, evt.After)
	assert.False(t, evt.AfterMsg.Empty())
}

// SERVER MODE, which is what the user ruling (translated: "and make sure
// everything is translated in server mode too") is about: there is no web UI
// and the CLI may be a different
// build, so the daemon has to answer an unmarked request in the language the
// box was configured with. That is daemon.yml's locale, the same setting the
// CLI reads its own strings from.
func TestAnUnmarkedRequestFollowsTheDaemonConfigLocale(t *testing.T) {
	d := &Daemon{daemonCfg: config.DaemonConfig{Locale: "de"}}
	assert.Equal(t, "de", d.translatorFor(httptest.NewRequest(http.MethodGet, "/api/v1/dependencies", http.NoBody)).Locale())
	// A stated locale still wins — a desktop UI and a CLI can be talking to
	// one daemon at the same moment.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dependencies", http.NoBody)
	req.Header.Set(api.LocaleHeader, "ja")
	assert.Equal(t, "ja", d.translatorFor(req).Locale())

	// And it reaches a real route. This is server mode exactly as it runs: no
	// header, because nothing on the box sends one, and the answer is still in
	// the operator's language rather than in English.
	dd, mux := localeProbeDaemon(t)
	dd.daemonCfg = config.DaemonConfig{Locale: "de"}
	pre := decodePreflight(t, depsGet(mux, api.DependencyPreflightPath("postgres")))
	require.Len(t, pre.Problems, 1)
	assert.Equal(t, i18n.NewTranslator("de").T(translatedProbeKey), pre.Problems[0])
	assert.NotEqual(t, i18n.NewTranslator("en").T(translatedProbeKey), pre.Problems[0],
		"an unmarked server-mode request must not fall back to English")
}

// The wire shape is what makes this change invisible downstream, and therefore
// what nothing but a test can protect: same field names, same types, arrays
// that are never null.
func TestPreflightWireShapeIsUnchanged(t *testing.T) {
	res := migrate.NewPreflightResult("postgres:17.5", "postgres:18")
	res.OK = true
	res.WasRunning = true
	res.SpaceChecked = true
	res.DataSizeBytes = 1 << 30
	res.RequiredHostBytes = 2 << 30
	res.RequiredVolumeBytes = 3 << 30
	res.FreeHostBytes = 4 << 30
	res.FreeVolumeBytes = 5 << 30
	res.SharedFilesystem = true
	res.RequiredTotalBytes = 6 << 30
	res.ExistingTargetVolume = &api.ExistingVolume{Name: "citeck_postgres3", SizeBytes: 7, Version: "18"}
	res.Warnings = append(res.Warnings, migrate.RetainedVolumeGoneProblem(deps.Postgres, "v", "i"))

	raw, err := json.Marshal(renderPreflight(i18n.NewTranslator("en"), res))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	// Exactly the fields the CLI decodes and the dialog reads — no more (a new
	// one is a new contract) and no fewer.
	want := []string{
		"ok", "problems", "warnings", "from", "to", "dataSizeBytes",
		"requiredHostBytes", "requiredVolumeBytes", "freeHostBytes", "freeVolumeBytes",
		"sharedFilesystem", "requiredTotalBytes", "existingTargetVolume", "wasRunning", "spaceChecked",
	}
	assert.ElementsMatch(t, want, keysOf(got))
	assert.IsType(t, []any{}, got["problems"])
	assert.IsType(t, []any{}, got["warnings"])
	// Every value too, not just the field names: a renderer that copies a
	// field to the wrong place, or forgets one, leaves the SHAPE intact and
	// the number wrong — which is the failure mode a shape-only test invites.
	assert.Equal(t, map[string]any{
		"ok": true, "from": "postgres:17.5", "to": "postgres:18",
		"dataSizeBytes": float64(1 << 30), "requiredHostBytes": float64(2 << 30),
		"requiredVolumeBytes": float64(3 << 30), "freeHostBytes": float64(4 << 30),
		"freeVolumeBytes": float64(5 << 30), "sharedFilesystem": true,
		"requiredTotalBytes": float64(6 << 30), "wasRunning": true, "spaceChecked": true,
		"problems": []any{},
		"warnings": []any{englishForLogs.Render(migrate.RetainedVolumeGoneProblem(deps.Postgres, "v", "i"))},
		"existingTargetVolume": map[string]any{
			"name": "citeck_postgres3", "sizeBytes": float64(7), "version": "18",
		},
	}, got)

	// The msg.Message fields must not have leaked onto the wire anywhere.
	assert.NotContains(t, string(raw), "deps.msg.")
	assert.NotContains(t, string(raw), "Key")
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The migration verdict a namespace persisted has TWO shapes and the renderer
// has to prefer the right one: a result written by an older launcher carries
// only the English string, and a step failure (a docker error, a psql stderr)
// legitimately has no sentence at all.
func TestResultDtoPrefersTheStructuredVerdictAndFallsBackToTheString(t *testing.T) {
	ru := i18n.NewTranslator("ru")

	// The two carry DIFFERENT text on purpose: the English string is what an
	// older launcher wrote and what the logs keep, and the assertion has to be
	// able to tell which of the two the renderer chose.
	structured := &deps.MigrationResult{
		ID: deps.Postgres, Error: "the persisted English, as an older launcher wrote it",
		ErrorMsg: msg.New("deps.msg.result.interruptedRolledBack"),
	}
	assert.Equal(t, ru.T("deps.msg.result.interruptedRolledBack"), resultDto(ru, structured).Error)
	assert.NotEqual(t, structured.Error, resultDto(ru, structured).Error)

	// No structured form — an older launcher's record, or a raw step failure.
	legacy := &deps.MigrationResult{ID: deps.Postgres, Error: "step restore: exit 1"}
	assert.Equal(t, "step restore: exit 1", resultDto(ru, legacy).Error)

	assert.Nil(t, resultDto(ru, nil))
}

// A plan refusal travels as an error, so it has its own rendering path — and
// Error() is the LOG's, naming keys. Quoting Error() into the response body is
// the regression this pins.
func TestPlanRefusalIsRenderedForTheOperatorNotLogged(t *testing.T) {
	err := error(&migrate.PlanRefusedError{Problems: []msg.Message{
		migrate.NotRegisteredProblem(deps.Postgres),
		migrate.RetainedVolumeGoneProblem(deps.Postgres, "postgres2", "postgres:17.5"),
	}})

	en := renderPlanError(i18n.NewTranslator("en"), err)
	assert.Contains(t, en, "is not a registered dependency")
	assert.Contains(t, en, "postgres2")
	assert.NotContains(t, en, "deps.msg.", "the operator must not be shown locale keys")

	ru := renderPlanError(i18n.NewTranslator("ru"), err)
	assert.Contains(t, ru, "postgres2")

	// Anything that is NOT a plan refusal is already final text (a docker
	// error) and passes through unchanged.
	assert.Equal(t, "docker: no such volume",
		renderPlanError(i18n.NewTranslator("ru"), errNoSuchVolume{}))
}

type errNoSuchVolume struct{}

func (errNoSuchVolume) Error() string { return "docker: no such volume" }

// The edit gate tells "the retained volume is gone" from every other
// unavailable offer, and it has to keep doing so once the offer has been
// rendered into a language whose sentence shares no words with the English
// one. That is why the comparison is on the KEY.
func TestTheWayBackRecognizesAGoneVolumeInAnyLanguage(t *testing.T) {
	desc, ok := deps.Lookup(deps.Postgres)
	require.True(t, ok)
	r := dependencyEditRefusal{desc: desc, app: "postgres", pinned: "postgres:18.6", wanted: "postgres:17.5"}

	offer := &api.DependencyRollbackDto{
		ToImage: "postgres:17.5", Volume: "postgres2",
		ProblemMsg: migrate.RetainedVolumeGoneProblem(deps.Postgres, "postgres2", "postgres:17.5"),
	}
	// Rendered in Russian first, exactly as the list route would have done.
	renderRollbackOffer(i18n.NewTranslator("ru"), offer)
	require.NotEmpty(t, offer.Problem)

	back := r.wayBack(offer)
	assert.Equal(t, "deps.msg.rollback.volumeGone", back.Key,
		"the gone-volume arm must be recognized by key, not by its prose")

	// An offer whose problem is a DIFFERENT sentence still falls through to
	// the hedged wording — "I could not ask" is not "it is gone".
	unknown := &api.DependencyRollbackDto{
		ToImage: "postgres:17.5", Volume: "postgres2",
		ProblemMsg: migrate.VolumeCheckProblem("postgres2", context.DeadlineExceeded),
	}
	assert.Equal(t, "deps.msg.edit.wayBackHedged", r.wayBack(unknown).Key)
}
