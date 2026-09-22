package namespace

import (
	"maps"
	"slices"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// AppPresence is what the caller could find out about an app the generator has
// just produced for the first time: does something of it already exist on this
// stand?
//
// The three answers are not interchangeable, for the same reason the dependency
// seeding keeps seedNoData and seedUnknown apart: "there is nothing here" is a
// fact the decision may act on, while "I could not ask" must not be turned into
// one — and the two mistakes are not symmetric. Detaching an app the operator
// is actually running is the expensive one.
type AppPresence int

const (
	// AppPresenceAbsent means no runtime entry and no container: the app really
	// is new to this stand.
	AppPresenceAbsent AppPresence = iota
	// AppPresencePresent means the app is in the runtime, or a container of it
	// exists on the host. Whoever it is new to, it is not new to this operator.
	AppPresencePresent
	// AppPresenceUnknown means the probe failed (Docker down, a refused
	// inspect). Nothing is decided and nothing is recorded, so the next pass
	// asks again.
	AppPresenceUnknown
)

// baselineKnownApps is the set of app ids EVERY stand has been running for
// releases: the answer to "which apps has this namespace already seen?" for a
// namespace whose state file predates KnownApps, i.e. every namespace in the
// field today.
//
// What it leaves out is the point. `qdrant`, `rag` and `stt-sidecar` arrive
// with a release; so do `observer` and its database, which were generatable
// before only behind a namespace.yml flag that defaulted to OFF — so on a stand
// that never set it, they have never existed, and a bundle that starts naming
// the observer image must offer them rather than start them. A stand that DID
// set the flag is covered from the other side: its containers exist, the
// presence probe says so, and they are recorded as known instead of detached.
//
// It is FROZEN. A new app must never be added to it: the whole point is that an
// app outside this list is one the operator has not been offered yet, so adding
// tomorrow's app here is exactly the auto-start this machinery exists to
// prevent. New apps are recorded per namespace, in NsPersistedState.KnownApps.
//
// It does not have to be exhaustive, and cannot be: a workspace may declare
// webapps and additionalApps this repository has never heard of. An id missing
// from it is treated as new, and the presence probe is what keeps that harmless
// — an app the operator is already running is recorded as known instead of
// being detached.
var baselineKnownApps = []string{
	appdef.AppProxy, appdef.AppGateway, appdef.AppEapps, appdef.AppEmodel,
	appdef.AppUiserv, appdef.AppHistory, appdef.AppNotifications,
	appdef.AppTransformations, appdef.AppEproc, appdef.AppPostgres,
	appdef.AppZookeeper, appdef.AppRabbitmq, appdef.AppMongodb, appdef.AppMailpit,
	appdef.AppKeycloak, appdef.AppPgadmin, appdef.AppOnlyoffice, appdef.AppAlfresco,
	appdef.AppAlfPostgres, appdef.AppAlfSolr, appdef.AppContent, appdef.AppAi,
	// Enterprise webapps that have no appdef constant but shipped long before
	// the companions did.
	"integrations", "ecom", "service-desk", "edi", "attorneys", "ecos-project-tracker",
}

// BaselineKnownApps returns the frozen pre-companion app set. Copy, so no
// caller can grow the baseline by accident.
func BaselineKnownApps() []string { return slices.Clone(baselineKnownApps) }

// NewAppDetachInput asks: of the apps this generation produced, which ones are
// arriving for the first time, and which of those should start life detached?
type NewAppDetachInput struct {
	// Known is what the namespace has already seen (NsPersistedState.KnownApps).
	// Empty means the state predates the field and the baseline stands in.
	Known []string
	// Generated is every app name this generation emitted.
	Generated []string
	// TemplateDetached is detachedApps of the workspace template the namespace
	// was created from. Empty (no template, no such template any more, no list)
	// means nothing is detached and the pass only records what it saw.
	TemplateDetached []string
	// TemplateStale says the list above could NOT be refreshed — the workspace
	// repo would not sync — so it may be older than the bundle that introduced
	// the candidate. An app the stale list already names is still decided from
	// it (that entry is the operator's intent either way); one it does NOT name
	// is deferred, because "the list has no entry" and "the list is out of
	// date" are indistinguishable here, and recording the app as known would
	// settle the question on the reading that may well be wrong.
	TemplateStale bool
	// Presence answers for one candidate app; nil reads as AppPresenceAbsent.
	Presence func(app string) AppPresence
}

// NewAppDetachResult is what the caller must do with the answer.
type NewAppDetachResult struct {
	// Detach names the new apps the template says are off by default. The
	// caller adds them to the operator's detach set — that set, and not a
	// second kind of "detached", is deliberately where this lands.
	Detach []string
	// Known is the set to persist. It is a UNION and never shrinks: an app can
	// leave the generated set for reasons that say nothing about the operator
	// (a bundle that resolved to nothing, a cached-bundle fallback, an app
	// switched off in namespace.yml), and forgetting it there would make it
	// "new" again when it comes back, i.e. detach an app the operator has been
	// running for months.
	Known []string
	// Deferred names candidates whose presence could not be determined. They
	// are NOT in Known, so the next pass asks again rather than silently
	// treating an unanswered probe as a decision.
	Deferred []string
}

// DecideNewAppDetach applies the rule. It is pure: every fact it needs is in
// the input, which is what makes the three traps below testable without a
// daemon, a bundle or Docker.
func DecideNewAppDetach(in NewAppDetachInput) NewAppDetachResult {
	known := make(map[string]bool, len(in.Known))
	for _, name := range in.Known {
		known[name] = true
	}
	if len(known) == 0 {
		for _, name := range baselineKnownApps {
			known[name] = true
		}
	}
	tmpl := make(map[string]bool, len(in.TemplateDetached))
	for _, name := range in.TemplateDetached {
		tmpl[name] = true
	}

	next := maps.Clone(known)
	var res NewAppDetachResult
	for _, app := range slices.Sorted(slices.Values(in.Generated)) {
		if known[app] {
			continue
		}
		if !tmpl[app] {
			if in.TemplateStale {
				// Same rule as an unanswered presence probe: decide nothing,
				// record nothing, ask again next pass.
				res.Deferred = append(res.Deferred, app)
				continue
			}
			// New, but the template says nothing about it: it starts like any
			// other app, and it is known from now on.
			next[app] = true
			continue
		}
		presence := AppPresenceAbsent
		if in.Presence != nil {
			presence = in.Presence(app)
		}
		switch presence {
		case AppPresencePresent:
			next[app] = true
		case AppPresenceUnknown:
			res.Deferred = append(res.Deferred, app)
		case AppPresenceAbsent:
			res.Detach = append(res.Detach, app)
			next[app] = true
		}
	}
	res.Known = slices.Sorted(maps.Keys(next))
	return res
}
