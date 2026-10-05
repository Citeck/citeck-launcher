package cli

import (
	"slices"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/output"
)

// The CLI's share of S3 content storage on a running namespace: the
// `citeck status` line and the warning on stopping the provider. Both read
// api.NamespaceDto.ContentStorage, which the daemon fills for an S3 namespace
// only — a DB namespace (every namespace that predates the option) prints
// nothing and warns about nothing.

// contentStorageStatusLine renders the value of the `citeck status`
// "Content storage:" line, or "" for a DB namespace (and for a daemon too old
// to report one).
//
// A generation PROBLEM outranks the connection state: the provider is not
// wired at all, so whatever the connection would do next is moot until the
// configuration is fixed — and it is the one condition no amount of waiting
// resolves.
func contentStorageStatusLine(ns *api.NamespaceDto) string {
	if ns == nil || ns.ContentStorage == nil {
		return ""
	}
	cs := ns.ContentStorage
	label := namespace.S3OptionLabel(cs.ProviderTitle, cs.ProviderApp)
	if cs.Problem != "" {
		return label + " — " + output.Colorize(output.Red,
			t("cli.contentStorage.problem", "reason", contentStorageProblemText(cs.Problem, cs.ProviderApp)))
	}
	switch cs.State {
	case api.ContentStorageStateConnected:
		return label + " — " + output.Colorize(output.Green, t("cli.contentStorage.state.connected"))
	case api.ContentStorageStateAdminManaged:
		return label + " — " + t("cli.contentStorage.state.adminManaged")
	case api.ContentStorageStateError:
		return label + " — " + output.Colorize(output.Red, t("cli.contentStorage.state.error", "err", cs.Error))
	case api.ContentStorageStateWaiting:
		return label + " — " + output.Colorize(output.Yellow, t("cli.contentStorage.state.waiting"))
	case api.ContentStorageStateProvisioning:
		return label + " — " + output.Colorize(output.Yellow, t("cli.contentStorage.state.provisioning"))
	case api.ContentStorageStatePending, "":
		if ns.Status == string(namespace.NsStatusStopped) {
			// Not a wait: nothing happens on a stopped namespace, and the
			// daemon keeps no "connected" flag, so every S3 namespace reads
			// pending while stopped — and after every daemon restart —
			// connected or not. Said neutrally, as the web banner does by
			// showing nothing.
			return label + " — " + t("cli.contentStorage.state.checkedOnStart")
		}
		return label + " — " + output.Colorize(output.Yellow, t("cli.contentStorage.state.pending"))
	default:
		// A state a newer daemon knows and this CLI does not: say it as it
		// came rather than guess what it means.
		return label + " — " + cs.State
	}
}

// contentStorageProblemText words a generation problem (the reason codes of
// namespace.ContentStorageProblem). An unknown code is printed as it came.
func contentStorageProblemText(code, providerApp string) string {
	switch code {
	case namespace.ContentStorageNoContentApp:
		return t("cli.contentStorage.problem.noContentApp")
	case namespace.ContentStorageNoProviderConfig:
		return t("cli.contentStorage.problem.noProviderConfig")
	case namespace.ContentStorageNoProviderApp:
		return t("cli.contentStorage.problem.noProviderApp", "app", providerApp)
	case namespace.ContentStorageNoProviderImage:
		return t("cli.contentStorage.problem.noProviderImage", "app", providerApp)
	case namespace.ContentStorageProviderConflict:
		return t("cli.contentStorage.problem.providerConflict", "app", providerApp)
	case namespace.ContentStorageProviderNotGenerated:
		return t("cli.contentStorage.problem.providerNotGenerated", "app", providerApp)
	default:
		return code
	}
}

// contentStorageStopWarnings returns the warning for every stopped app that
// is the S3 provider of this namespace — nil for a DB namespace or any other
// app. A warning, never a refusal (owner ruling): stopping the provider is
// legitimate — to run it elsewhere, to free memory for a while — and the
// launcher honors it; ecos-content then waits for it, and the operator should
// know that uploads and downloads fail meanwhile.
func contentStorageStopWarnings(ns *api.NamespaceDto, stopped []string) []string {
	if ns == nil || ns.ContentStorage == nil || ns.ContentStorage.ProviderApp == "" {
		return nil
	}
	app := ns.ContentStorage.ProviderApp
	if !slices.Contains(stopped, app) {
		return nil
	}
	return []string{t("cli.contentStorage.stopWarning", "app", app)}
}
