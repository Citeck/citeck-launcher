package setup

import (
	"errors"
	"fmt"
	"slices"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// contentStorageSetting is `citeck setup content-storage`: where ecos-content
// keeps its files. It offers exactly one move, DB → S3, because S3 is one-way
// (the files written to S3 are not in the database, and nothing moves them
// back), and it is offered only where the bundle has ecos-content at all.
//
// The switch is not a file edit like the other settings. It needs the S3
// provider — which the default workspace template keeps detached — to be
// re-attached once, and the namespace reloaded so ecos-content waits for it.
// So it is written by the DAEMON, through its single write path (PUT /config):
// the daemon's write gate decides whether the bundle may have S3 (the check
// here is a courtesy that explains a refusal before any question is asked),
// and the daemon re-attaches the provider and reloads, under the locks every
// other namespace mutation takes, before it answers. With no daemon running
// there is nobody to do that, and the setting says so instead of writing a
// file the provider would stay detached under.
type contentStorageSetting struct{}

func (s *contentStorageSetting) ID() string              { return "content-storage" }
func (s *contentStorageSetting) Title() string           { return i18n.T("setup.content_storage.title") }
func (s *contentStorageSetting) Description() string     { return i18n.T("setup.content_storage.desc") }
func (s *contentStorageSetting) TargetFile() TargetFile  { return NamespaceFile }
func (s *contentStorageSetting) isDaemonAppliedSetting() {}

// Available: a bundle without ecos-content (Community) has no content storage
// to choose — the question does not exist there.
func (s *contentStorageSetting) Available(_ *namespace.Config, apps []string) bool {
	return slices.Contains(apps, appdef.AppContent)
}

func (s *contentStorageSetting) CurrentValue(cfg *namespace.Config, _ *config.DaemonConfig) string {
	if cfg.ContentStorageType() == namespace.ContentStorageS3 {
		return i18n.T("setup.content_storage.value.s3")
	}
	return i18n.T("setup.content_storage.value.db")
}

// daemonReachable answers whether a daemon is there to apply the switch.
// A variable so the setting's refusal is testable without a socket.
var daemonReachable = daemonIsRunning

func (s *contentStorageSetting) Run(ctx *setupContext, cfg *namespace.Config, _ *config.DaemonConfig) error {
	avail, refusal := contentStorageSwitchCheck(ctx, cfg)
	providerApp := avail.ProviderApp
	if refusal == "" && !daemonReachable() {
		refusal = i18n.T("setup.content_storage.daemon_required", "app", providerApp)
	}
	if refusal != "" {
		fmt.Println(refusal) //nolint:forbidigo // CLI output
		return prompt.ErrCanceled
	}

	choice, err := (&prompt.Select[string]{
		Title:       i18n.T("setup.content_storage.title"),
		Description: i18n.T("setup.content_storage.hint", "app", providerApp),
		Options: []prompt.Option[string]{
			{Label: i18n.T("setup.content_storage.db"), Value: string(namespace.ContentStorageDB), Hint: i18n.T("setup.content_storage.current")},
			{Label: avail.OptionLabel(), Value: string(namespace.ContentStorageS3), Hint: i18n.T("setup.content_storage.one_way")},
			{Label: i18n.T("setup.back"), Value: backValue},
		},
		Hints: hints(),
	}).Run()
	if err != nil {
		return fmt.Errorf("content storage selection: %w", err)
	}
	switch choice {
	case string(namespace.ContentStorageS3):
		cfg.ContentStorage = &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}
		return nil
	case string(namespace.ContentStorageDB):
		return nil // the current value: nothing changes
	default:
		return prompt.ErrCanceled
	}
}

// contentStorageSwitchCheck answers whether this namespace may switch to S3
// now, and the availability whose provider it would run. refusal is the
// operator's sentence when it may not, "" when it may. Pure: everything it
// reads was resolved when setup started.
//
// The availability sentences are the daemon's own (contentStorage.msg.*, the
// write gate's wording, one asset for both), so the refusal reads the same
// whether setup explains it up front or the daemon refuses the write.
func contentStorageSwitchCheck(ctx *setupContext, cfg *namespace.Config) (avail namespace.ContentStorageAvailability, refusal string) {
	switch {
	case cfg.ContentStorageType() == namespace.ContentStorageS3:
		return avail, i18n.T("contentStorage.msg.downgrade")
	case cfg.S3 != nil:
		// The validation refuses the two together anyway; said here in the
		// operator's terms instead of after the questions.
		return avail, i18n.T("setup.content_storage.external_s3")
	case ctx == nil || ctx.Bundle == nil:
		return avail, i18n.T("setup.content_storage.bundle_unresolved", "bundle", cfg.BundleRef.String())
	}
	avail = namespace.ContentStorageS3Availability(ctx.Bundle, ctx.Workspace)
	if !avail.Available {
		return avail, contentStorageUnavailableSentence(avail, cfg.BundleRef)
	}
	return avail, ""
}

// contentStorageUnavailableSentence words an availability refusal with the
// write gate's keys (internal/daemon contentStorageUnavailableMessage).
func contentStorageUnavailableSentence(avail namespace.ContentStorageAvailability, ref bundle.Ref) string {
	switch avail.Reason {
	case namespace.ContentStorageNoContentApp:
		return i18n.T("contentStorage.msg.unavailable.noContentApp", "bundle", ref.String())
	case namespace.ContentStorageNoProviderConfig:
		return i18n.T("contentStorage.msg.unavailable.noProviderConfig")
	case namespace.ContentStorageNoProviderApp:
		return i18n.T("contentStorage.msg.unavailable.noProviderApp", "app", avail.ProviderApp)
	case namespace.ContentStorageNoProviderImage:
		return i18n.T("contentStorage.msg.unavailable.noProviderImage", "app", avail.ProviderApp, "bundle", ref.String())
	case namespace.ContentStorageProviderConflict:
		return i18n.T("contentStorage.msg.unavailable.providerConflict", "app", avail.ProviderApp)
	default:
		return i18n.T("setup.content_storage.bundle_unresolved", "bundle", ref.String())
	}
}

// namespaceConfigWriter is the daemon surface the switch needs: its single
// namespace-config write path. Narrowed so writeThroughDaemon is testable
// without a daemon.
type namespaceConfigWriter interface {
	PutNamespaceConfig(body []byte) (*api.ActionResultDto, error)
}

// daemonWrite is how a daemon write of the switch ended.
type daemonWrite int

const (
	// daemonWriteApplied: the daemon wrote the config, re-attached the
	// provider and reloaded.
	daemonWriteApplied daemonWrite = iota
	// daemonWriteNotApplied: the request failed, but the config on disk now
	// says S3 — the daemon wrote it and then failed (its reload, or the
	// connection after the write). The switch has happened; applying it has not.
	daemonWriteNotApplied
	// daemonWriteRefused: nothing was written.
	daemonWriteRefused
)

// writeThroughDaemon hands the new namespace.yml to the daemon and says how
// that ended, with the detail to print for anything but daemonWriteApplied.
//
// A failed request is NOT proof that nothing was written: the daemon answers a
// failed reload with a 500 after the write, and a connection can drop after it
// too. The file is the evidence — in server mode the daemon's write IS
// namespace.yml — so written() is asked whenever the request failed, and only a
// config that does not say S3 counts as refused. A refusal is the daemon's own
// sentence, already in the operator's language; anything else is reported as
// it came.
func writeThroughDaemon(w namespaceConfigWriter, data []byte, written func() bool) (outcome daemonWrite, detail string) {
	if w == nil {
		return daemonWriteRefused, i18n.T("cli.daemonNotRunningStart")
	}
	_, err := w.PutNamespaceConfig(data)
	if err == nil {
		return daemonWriteApplied, ""
	}
	detail = err.Error()
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Message != "" {
		detail = apiErr.Message
	}
	if written() {
		return daemonWriteNotApplied, detail
	}
	return daemonWriteRefused, detail
}

// storedConfigIsS3 reads namespace.yml back and answers whether it stores the
// content in S3 — writeThroughDaemon's evidence of a write.
func storedConfigIsS3(cfgPath string) func() bool {
	return func() bool {
		cfg, err := namespace.LoadNamespaceConfig(cfgPath)
		return err == nil && cfg.ContentStorageType() == namespace.ContentStorageS3
	}
}

// applyThroughDaemon is runSingleSetting's write step for a daemon-applied
// setting: the bridge record (taken BEFORE the daemon overwrites the file), the
// daemon's write, the history entry, and the wait for the reload the daemon
// started. A refusal restores the in-memory config and records nothing; a
// write the daemon could not apply is recorded (it happened) and says how to
// finish it.
func applyThroughDaemon(id string, sctx *setupContext, nsCfg *namespace.Config, daemonCfg *config.DaemonConfig,
	nsBefore *namespace.Config, daemonBefore *config.DaemonConfig, forward, reverse []PatchOp, restore func(),
) error {
	data, err := namespace.MarshalNamespaceConfig(nsCfg)
	if err != nil {
		return fmt.Errorf("marshal namespace config: %w", err)
	}
	cfgPath := configFilePath(NamespaceFile)
	histDir := historyDir(cfgPath)
	providerApp := ""
	if apps := namespace.RequiredApps(nsCfg, sctx.Workspace); len(apps) > 0 {
		providerApp = apps[0]
	}

	var w namespaceConfigWriter // stays a nil interface, never a typed nil, without a daemon
	c := client.TryNew(client.Options{})
	if c != nil {
		defer c.Close()
		w = c
		recordBridge(NamespaceFile, histDir, nsBefore, daemonBefore)
	}
	outcome, detail := writeThroughDaemon(w, data, storedConfigIsS3(cfgPath))
	if outcome == daemonWriteRefused {
		fmt.Println(i18n.T("setup.content_storage.daemon_failed", "err", detail)) //nolint:forbidigo // CLI output
		restore()
		return nil
	}

	recordSettingPatch(id, histDir, NamespaceFile, forward, reverse, nsCfg, daemonCfg, nil)
	fmt.Println(i18n.T("setup.applied")) //nolint:forbidigo // CLI output
	if outcome == daemonWriteNotApplied {
		fmt.Println(i18n.T("setup.content_storage.not_applied", "err", detail, "app", providerApp)) //nolint:forbidigo // CLI output
		return nil
	}
	status := ""
	if ns, nsErr := c.GetNamespace(); nsErr == nil && ns != nil {
		status = ns.Status
	}
	note, wait := contentStorageSwitchedNote(status, providerApp)
	fmt.Println(note) //nolint:forbidigo // CLI output
	if wait {
		waitForServices(c)
	}
	return nil
}

// contentStorageSwitchedNote is what the operator is told once the daemon has
// applied the switch, and whether there is a reload to wait for. On a STOPPED
// namespace the daemon only cleared the provider's detach and regenerated:
// nothing starts until the namespace does, so "is starting" would be untrue
// and the wait would report every app as updated while all of them are
// stopped. An unknown status is treated as running — the old behavior.
func contentStorageSwitchedNote(nsStatus, providerApp string) (note string, wait bool) {
	if nsStatus == string(namespace.NsStatusStopped) {
		return i18n.T("setup.content_storage.switched_stopped", "app", providerApp), false
	}
	return i18n.T("setup.content_storage.switched", "app", providerApp), true
}
