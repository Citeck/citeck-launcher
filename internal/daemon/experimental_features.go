package daemon

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// Experimental (BETA) features: a bundle's top-level `experimental:` section
// names the modules a release ships as beta (bundle.Def.Experimental); a
// namespace opts into them by feature id (namespace.Config.ExperimentalFeatures).
// This file is the daemon's side of it — what a dialog may offer for a bundle,
// and what a create or edit request may switch on.
//
// Two rules shape every function here. (1) Offers are resolved OFFLINE, like
// the content-storage options, so the dialog shows exactly what saving will
// accept. (2) Only the ids a request ADDS are checked against the bundle; a
// stored id the bundle does not offer is kept and inert, because the bundle
// that stops offering a feature is usually the release that graduated the
// module to a regular application — and refusing that upgrade would be the
// one outcome the whole mechanism must never produce.

// offeredExperimentalFeatures resolves the bundle offline and answers the BETA
// features it offers; ok is false when the bundle cannot be read from disk.
func (d *Daemon) offeredExperimentalFeatures(wsID string, ref bundle.Ref) ([]bundle.ExperimentalFeature, bool) {
	if ref.IsEmpty() {
		return nil, false
	}
	res, err := d.resolveBundleOffline(wsID, ref)
	if err != nil || res == nil || res.Bundle == nil {
		return nil, false
	}
	return res.Bundle.ExperimentalFeatures(), true
}

// experimentalFeatureDtos renders the offers with the given namespace choices.
func experimentalFeatureDtos(features []bundle.ExperimentalFeature, enabled map[string]bool) []api.ExperimentalFeatureDto {
	out := make([]api.ExperimentalFeatureDto, 0, len(features))
	for _, f := range features {
		out = append(out, api.ExperimentalFeatureDto{
			ID:          f.ID,
			Title:       f.Title,
			Description: f.Description,
			Apps:        f.Apps,
			Enabled:     enabled[f.ID],
		})
	}
	return out
}

// experimentalFeaturesForEdit is NamespaceEditDto.ExperimentalFeatures: what
// the STORED bundle offers, each with whether the namespace has it on. Nil when
// the bundle offers nothing or cannot be read, and the form shows no section.
func (d *Daemon) experimentalFeaturesForEdit(wsID string, nsCfg *namespace.Config) []api.ExperimentalFeatureDto {
	features, ok := d.offeredExperimentalFeatures(wsID, nsCfg.BundleRef)
	if !ok || len(features) == 0 {
		return nil
	}
	return experimentalFeatureDtos(features, nsCfg.ExperimentalEnabled())
}

// handleExperimentalFeatures answers GET /experimental-features: the BETA
// features a namespace on the given bundle may switch on. With no bundle in
// the query the active namespace's is asked, and when the bundle asked about
// IS the active namespace's, Enabled reflects its choices.
func (d *Daemon) handleExperimentalFeatures(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	wsID := q.Get("workspaceId")
	if wsID == "" {
		wsID = d.activeWorkspaceID()
	} else if !validateID(wsID) {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	ref := bundle.Ref{Repo: q.Get("bundleRepo"), Key: q.Get("bundleKey")}
	var enabled map[string]bool
	if act := d.active(); act.nsConfig != nil && act.workspaceID == wsID {
		if ref.IsEmpty() {
			ref = act.nsConfig.BundleRef
		}
		if ref == act.nsConfig.BundleRef {
			enabled = act.nsConfig.ExperimentalEnabled()
		}
	}
	features, ok := d.offeredExperimentalFeatures(wsID, ref)
	if !ok {
		writeJSON(w, api.ExperimentalFeaturesDto{Features: []api.ExperimentalFeatureDto{},
			Reason: api.ExperimentalFeaturesBundleUnresolved})
		return
	}
	writeJSON(w, api.ExperimentalFeaturesDto{Features: experimentalFeatureDtos(features, enabled)})
}

// applyCreateExperimentalFeatures sets the new namespace's features from the
// create REQUEST and from nothing else. A workspace template's list is dropped
// on purpose, like its contentStorage: a beta module is the operator's choice
// to make in the dialog, and a template that made it would switch on a module
// the form never showed. Whether the chosen bundle offers the ids is the
// caller's question, once the bundle key is pinned (see
// checkCreateExperimentalFeatures).
func applyCreateExperimentalFeatures(nsCfg *namespace.Config, requested []string) {
	if len(nsCfg.ExperimentalFeatures) > 0 {
		slog.Warn("The namespace template sets experimentalFeatures; ignored — the beta modules of a new namespace are the operator's choice",
			"template", nsCfg.Template, "features", nsCfg.ExperimentalFeatures)
	}
	nsCfg.ExperimentalFeatures = namespace.NormalizeExperimentalFeatures(requested)
}

// checkCreateExperimentalFeatures refuses a create whose features the resolved
// bundle does not offer. A create has no stored list, so every id is an ADDED
// one and every id is checked.
func checkCreateExperimentalFeatures(nsCfg *namespace.Config, bun *bundle.Def) *createNamespaceError {
	if len(nsCfg.ExperimentalFeatures) == 0 {
		return nil
	}
	offered := bun.ExperimentalFeatures()
	unknown := unknownExperimentalFeatures(offered, nsCfg.ExperimentalFeatures)
	if len(unknown) == 0 {
		return nil
	}
	return &createNamespaceError{
		status:  http.StatusBadRequest,
		code:    api.ErrCodeExperimentalFeatureUnknown,
		message: experimentalFeatureUnknownMessage(unknown, offered, nsCfg.BundleRef),
	}
}

// applyEditExperimentalFeatures applies the edit form's choice onto the stored
// config, answering false (with the response written) when the request is
// refused. Nil leaves the list unchanged. The request carries the state of the
// features the bundle OFFERS; stored ids outside that set are kept — see
// api.NamespaceEditDto.EnabledExperimentalFeatures. It must run after the
// request's bundleRef change, so the ids are checked against the bundle the
// namespace will be on.
func (d *Daemon) applyEditExperimentalFeatures(w http.ResponseWriter, wsID string, current *namespace.Config, requested *[]string) bool {
	if requested == nil {
		return true
	}
	wanted := namespace.NormalizeExperimentalFeatures(*requested)
	offered, ok := d.offeredExperimentalFeatures(wsID, current.BundleRef)
	if !ok {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeExperimentalFeatureUnknown,
			fmt.Sprintf("cannot change experimental features: bundle %s is not available on disk, so what it offers cannot be verified; refresh the bundle list and retry",
				current.BundleRef.String()))
		return false
	}
	if unknown := unknownExperimentalFeatures(offered, wanted); len(unknown) > 0 {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeExperimentalFeatureUnknown,
			experimentalFeatureUnknownMessage(unknown, offered, current.BundleRef))
		return false
	}
	offeredIDs := make(map[string]bool, len(offered))
	for _, f := range offered {
		offeredIDs[f.ID] = true
	}
	// Stored ids the bundle does not offer are inert and survive the edit; the
	// offered ones take exactly the state the form sent.
	kept := make([]string, 0, len(current.ExperimentalFeatures)+len(wanted))
	for _, id := range current.ExperimentalFeatures {
		if !offeredIDs[id] {
			kept = append(kept, id)
		}
	}
	current.ExperimentalFeatures = namespace.NormalizeExperimentalFeatures(append(kept, wanted...))
	return true
}

// unknownExperimentalFeatures names the ids among `ids` that no offered feature
// carries, in request order.
func unknownExperimentalFeatures(offered []bundle.ExperimentalFeature, ids []string) []string {
	known := make(map[string]bool, len(offered))
	for _, f := range offered {
		known[f.ID] = true
	}
	unknown := make([]string, 0, len(ids))
	for _, id := range ids {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	return unknown
}

// experimentalFeatureUnknownMessage words the refusal: which ids the bundle
// does not offer, and which it does, so the caller can correct the request.
func experimentalFeatureUnknownMessage(unknown []string, offered []bundle.ExperimentalFeature, ref bundle.Ref) string {
	if len(offered) == 0 {
		return fmt.Sprintf("bundle %s offers no experimental features; cannot enable %s",
			ref.String(), strings.Join(unknown, ", "))
	}
	ids := make([]string, 0, len(offered))
	for _, f := range offered {
		ids = append(ids, f.ID)
	}
	return fmt.Sprintf("bundle %s does not offer experimental feature(s) %s; it offers: %s",
		ref.String(), strings.Join(unknown, ", "), strings.Join(ids, ", "))
}
