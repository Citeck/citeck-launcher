package cli

import (
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The install wizard's content-storage question. `citeck install` writes
// namespace.yml ITSELF, past the daemon's write gate, so the rule the gate
// enforces — S3 only where the bundle has ecos-content and the workspace a
// usable provider — has to hold here by construction: the question is asked
// only when namespace.ContentStorageS3Availability says yes, and the answer
// is written only as the block the gate would accept.
//
// Nothing else is needed to start the provider: the daemon's first load of a
// namespace with no persisted state subtracts the apps an S3 namespace
// requires from the template's detachedApps (namespace.RequiredApps).

// installContentStorageAvailability resolves the picked release OFFLINE —
// the release picker has just synced what there is to sync — and answers
// whether it may store its content in S3. A release that cannot be read is
// "not available": S3 cannot be switched back, so an unverified offer is the
// one way to be stuck with it.
func installContentStorageAvailability(ref bundle.Ref, launcherVersion string) namespace.ContentStorageAvailability {
	if ref.IsEmpty() {
		return namespace.ContentStorageAvailability{}
	}
	resolver := bundle.NewResolver(config.DataDir()).
		WithLogger(slog.New(slog.DiscardHandler)).
		WithLauncherVersion(launcherVersion)
	resolver.SetOffline(true)
	res, err := resolver.Resolve(ref)
	if err != nil || res == nil || res.Bundle == nil {
		return namespace.ContentStorageAvailability{}
	}
	return namespace.ContentStorageS3Availability(res.Bundle, res.Workspace)
}

// chooseInstallContentStorage is the sub-step's decision: no question at all
// unless S3 is available (Community has no ecos-content, and a workspace with
// no provider has nothing to offer), and the namespace.yml block for the
// answer — nil for DB, which is what every other namespace has: absent means
// DB. ask receives the availability, whose provider the S3 option runs.
func chooseInstallContentStorage(avail namespace.ContentStorageAvailability,
	ask func(avail namespace.ContentStorageAvailability) namespace.ContentStorageType,
) *namespace.ContentStorageProps {
	if !avail.Available {
		return nil
	}
	if ask(avail) != namespace.ContentStorageS3 {
		return nil
	}
	return &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}
}

// promptInstallContentStorage asks the question, with DB first and
// pre-selected: S3 is the choice that cannot be undone, so it is never the one
// a reflexive Enter makes. The S3 option is labeled the way the web dialog
// labels it ("S3 (SeaweedFS)", from the workspace's provider title).
func promptInstallContentStorage(avail namespace.ContentStorageAvailability) namespace.ContentStorageType {
	choice, err := (&prompt.Select[namespace.ContentStorageType]{
		Title:       t("install.contentStorage.label"),
		Description: t("install.contentStorage.hint", "app", avail.ProviderApp),
		Options: []prompt.Option[namespace.ContentStorageType]{
			{Label: t("install.contentStorage.db"), Value: namespace.ContentStorageDB, Hint: t("install.contentStorage.default")},
			{Label: avail.OptionLabel(), Value: namespace.ContentStorageS3},
		},
		Hints: i18nHints(),
	}).Run()
	abortOnCancel(err)
	if err != nil {
		return namespace.ContentStorageDB
	}
	return choice
}
