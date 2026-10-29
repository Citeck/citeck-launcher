package cli

import (
	"log/slog"
	"strings"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The install wizard's experimental-modules question. Like the content-storage
// sub-step it is asked only when the picked release has something to offer —
// a bundle with no `experimental:` section asks nothing — and defaults to
// NONE: a beta module is the operator's choice, never a reflexive Enter's.
// `citeck install` writes namespace.yml itself, past the daemon's create
// gate, so what it writes is what that gate would accept: ids the resolved
// bundle offers, normalized.

// installExperimentalFeatures resolves the picked release OFFLINE — the
// release picker has just synced what there is to sync — and answers the
// BETA features it offers. A release that cannot be read offers nothing.
func installExperimentalFeatures(ref bundle.Ref, launcherVersion string) []bundle.ExperimentalFeature {
	if ref.IsEmpty() {
		return nil
	}
	resolver := bundle.NewResolver(config.DataDir()).
		WithLogger(slog.New(slog.DiscardHandler)).
		WithLauncherVersion(launcherVersion)
	resolver.SetOffline(true)
	res, err := resolver.Resolve(ref)
	if err != nil || res == nil || res.Bundle == nil {
		return nil
	}
	return res.Bundle.ExperimentalFeatures()
}

// chooseInstallExperimentalFeatures is the sub-step's decision: no question
// unless the release offers a feature, and the namespace.yml list for the
// answer — nil when nothing was chosen, which is what every other namespace
// has. ask receives the offers and answers the chosen ids.
func chooseInstallExperimentalFeatures(offered []bundle.ExperimentalFeature,
	ask func(offered []bundle.ExperimentalFeature) []string,
) []string {
	if len(offered) == 0 {
		return nil
	}
	return namespace.NormalizeExperimentalFeatures(ask(offered))
}

// promptInstallExperimentalFeatures asks the question as a checklist with
// nothing marked: each row is the feature's title (and its id when the
// author gave it a title), the apps the switch turns on as the hint, and the
// author's description under it.
func promptInstallExperimentalFeatures(offered []bundle.ExperimentalFeature) []string {
	options := make([]prompt.Option[string], 0, len(offered))
	for _, f := range offered {
		label := f.Title
		if f.Title != f.ID {
			label += " (" + f.ID + ")"
		}
		options = append(options, prompt.Option[string]{
			Label:       label,
			Value:       f.ID,
			Hint:        "BETA · " + strings.Join(f.Apps, ", "),
			Description: f.Description,
		})
	}
	chosen, err := (&prompt.MultiSelect[string]{
		Title:       t("install.experimental.label"),
		Description: t("install.experimental.hint"),
		Options:     options,
		None:        t("install.experimental.none"),
		Height:      prompt.DefaultSelectHeight,
		Hints:       i18nHints(),
	}).Run()
	abortOnCancel(err)
	if err != nil {
		return nil
	}
	return chosen
}
