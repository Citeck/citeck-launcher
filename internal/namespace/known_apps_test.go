package namespace

import (
	"encoding/json"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The case the whole mechanism exists for: a bundle release adds an app to a
// namespace that has been running for months, the workspace template says that
// app is off by default, and it must NOT start itself.
func TestANewAppTheTemplateDetachesArrivesDetached(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Known:            []string{appdef.AppPostgres, appdef.AppGateway},
		Generated:        []string{appdef.AppPostgres, appdef.AppGateway, appdef.AppRag, appdef.AppQdrant},
		TemplateDetached: []string{appdef.AppRag},
	})

	assert.Equal(t, []string{appdef.AppRag}, res.Detach)
	assert.Contains(t, res.Known, appdef.AppQdrant,
		"an app the template says nothing about still becomes known — it starts, once")
	assert.NotContains(t, res.Detach, appdef.AppQdrant)
}

// A namespace whose state predates KnownApps must not have its whole app set
// read as new — that would detach every optional app the operator turned on by
// hand. The frozen baseline is what stands in, and everything in it is old.
func TestAnAbsentKnownSetFallsBackToTheBaselineRatherThanToNothing(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Generated:        []string{appdef.AppOnlyoffice, appdef.AppAi, appdef.AppContent, appdef.AppSttSidecar},
		TemplateDetached: []string{appdef.AppOnlyoffice, appdef.AppAi, appdef.AppContent, appdef.AppSttSidecar},
	})

	assert.Equal(t, []string{appdef.AppSttSidecar}, res.Detach,
		"only the app that postdates the baseline is new; the rest the operator has had for releases")
}

// The baseline is a historical fact, not a list of optional apps. If somebody
// adds tomorrow's app to it, that app auto-starts on every stand in the field —
// which is the exact thing this machinery prevents.
func TestTheBaselineExcludesEveryCompanionAppAndIsACopy(t *testing.T) {
	base := BaselineKnownApps()
	assert.NotContains(t, base, appdef.AppQdrant)
	assert.NotContains(t, base, appdef.AppRag)
	assert.NotContains(t, base, appdef.AppSttSidecar)
	assert.NotContains(t, base, appdef.AppObserver,
		"the observer was behind an off-by-default flag: on a stand that never set it, "+
			"it has never existed, and a bundle that starts naming it must OFFER it")
	assert.NotContains(t, base, appdef.AppObsPostgres)
	assert.Contains(t, base, appdef.AppAi, "ai shipped long before its sidecar did")

	base[0] = "mutated"
	assert.NotContains(t, BaselineKnownApps(), "mutated",
		"the baseline must not be growable through the accessor")
}

// The expensive mistake: detaching an app the operator is already running. A
// stand that predates the baseline entry runs stt-sidecar today, so presence —
// not the list — is what has the last word.
func TestAnAppAlreadyOnThisStandIsRecordedRatherThanDetached(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Generated:        []string{appdef.AppSttSidecar},
		TemplateDetached: []string{appdef.AppSttSidecar},
		Presence: func(string) AppPresence {
			return AppPresencePresent
		},
	})

	assert.Empty(t, res.Detach, "it is not new to this operator, whatever the list says")
	assert.Contains(t, res.Known, appdef.AppSttSidecar, "and it is not asked about again")
}

// "I could not ask" must never become "there is nothing there" — and it must
// not become a settled answer either, or a single Docker outage would decide
// the question for good.
func TestAnUnansweredProbeDecidesNothingAndIsRetried(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Generated:        []string{appdef.AppSttSidecar},
		TemplateDetached: []string{appdef.AppSttSidecar},
		Presence: func(string) AppPresence {
			return AppPresenceUnknown
		},
	})

	assert.Empty(t, res.Detach)
	assert.Equal(t, []string{appdef.AppSttSidecar}, res.Deferred)
	assert.NotContains(t, res.Known, appdef.AppSttSidecar,
		"recording it would turn one failed inspect into a permanent verdict")
}

// An app can leave the generated set for reasons that say nothing about the
// operator — a bundle that resolved to nothing, a cached-bundle fallback, an
// app switched off in namespace.yml. Forgetting it there would make it new
// again when it comes back, i.e. detach something long since running.
func TestTheKnownSetNeverShrinks(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Known:            []string{appdef.AppPostgres, appdef.AppRag, appdef.AppQdrant},
		Generated:        []string{appdef.AppPostgres, "brand-new"},
		TemplateDetached: []string{appdef.AppRag},
	})

	assert.Contains(t, res.Known, appdef.AppRag)
	assert.Contains(t, res.Known, appdef.AppQdrant)
	assert.Empty(t, res.Detach, "a known app is never re-detached, generated or not")
}

// A namespace with no template (or one the workspace no longer declares) has
// nobody to ask, and a new app there behaves exactly as it always has.
func TestWithNoTemplateNothingIsDetachedButEverythingIsRecorded(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Known:     []string{appdef.AppPostgres},
		Generated: []string{appdef.AppPostgres, appdef.AppRag},
	})

	assert.Empty(t, res.Detach)
	assert.Contains(t, res.Known, appdef.AppRag)
}

// The set has to survive a restart, or the next load would call the same app
// new again and undo the operator's start.
func TestKnownAppsRoundTripThroughPersistedState(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()
	p := &fakePersister{}
	r.SetStatePersister(p)

	r.SetKnownApps([]string{appdef.AppQdrant, appdef.AppPostgres})
	assert.Equal(t, []string{appdef.AppPostgres, appdef.AppQdrant}, r.KnownApps())

	r.mu.Lock()
	require.NoError(t, r.persistState())
	r.mu.Unlock()

	var state NsPersistedState
	require.NoError(t, json.Unmarshal([]byte(p.lastJSON()), &state))
	assert.Equal(t, []string{appdef.AppPostgres, appdef.AppQdrant}, state.KnownApps)
}

// A template list that could not be refreshed is not the same as a template
// that says nothing. The load where the workspace repo would not sync is
// exactly the load where the entry for the brand-new app may be the one
// missing, so the candidate is deferred rather than recorded — recording it
// would settle the question forever on the reading that may be wrong.
func TestAStaleTemplateListDefersTheAppsItDoesNotName(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Known:            []string{appdef.AppPostgres},
		Generated:        []string{appdef.AppPostgres, appdef.AppRag, appdef.AppAi},
		TemplateDetached: []string{appdef.AppAi},
		TemplateStale:    true,
	})

	assert.Equal(t, []string{appdef.AppRag}, res.Deferred)
	assert.NotContains(t, res.Known, appdef.AppRag)
	assert.Equal(t, []string{appdef.AppAi}, res.Detach,
		"an app the stale list DOES name is decided from it: that entry is the intent either way")
}

// The same list, known to be current, decides: an app it does not name starts
// and is never asked about again. Without this the two arms would be
// indistinguishable and every load would re-ask about every optional app.
func TestAFreshTemplateListRecordsTheAppsItDoesNotName(t *testing.T) {
	res := DecideNewAppDetach(NewAppDetachInput{
		Known:            []string{appdef.AppPostgres},
		Generated:        []string{appdef.AppPostgres, appdef.AppRag},
		TemplateDetached: []string{appdef.AppAi},
	})

	assert.Empty(t, res.Deferred)
	assert.Contains(t, res.Known, appdef.AppRag)
	assert.Empty(t, res.Detach)
}
