package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/msg"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// Content storage, the daemon's half: the write gate every namespace-config
// write passes, the options the create/edit dialogs offer, the explicit
// re-attach of the provider when an operator switches DB → S3, and what the
// namespace DTO reports. The rule for which bundles may have S3 is
// namespace.ContentStorageS3Availability; the one-time connection in ECOS is
// content_storage_provision.go.

// errContentStorageRefused carries a content-storage refusal out of
// persistNamespaceConfig so each route can render it in the reader's language
// (writeConfigGateRefusal), the way errLauncherTooOld travels.
type errContentStorageRefused struct {
	// code is api.ErrCodeContentStorageUnavailable or
	// api.ErrCodeContentStorageDowngrade.
	code string
	// reason is the availability reason for an unavailable refusal.
	reason  string
	message msg.Message
}

func (e *errContentStorageRefused) Error() string {
	if e.reason != "" {
		return fmt.Sprintf("content storage refused (%s): %s", e.code, e.reason)
	}
	return fmt.Sprintf("content storage refused (%s)", e.code)
}

// errStoredConfigUnreadable is a write the gate could not judge because the
// config it would replace could not be read from the store. Refused — failing
// CLOSED: answering "DB" for a config nobody could read would let any write
// pass as DB → DB and silently undo a one-way S3 choice.
type errStoredConfigUnreadable struct{ err error }

func (e *errStoredConfigUnreadable) Error() string {
	return fmt.Sprintf("read the stored namespace config: %v", e.err)
}

func (e *errStoredConfigUnreadable) Unwrap() error { return e.err }

// writeConfigGateRefusal answers the write-gate refusals persistNamespaceConfig
// can return — a bundle that needs a newer launcher, a content-storage choice
// that cannot be written, a stored config the gate could not read — and
// reports whether it did. Every write route (create, edit, upgrade, raw config
// PUT) calls it before its own error mapping.
func (d *Daemon) writeConfigGateRefusal(w http.ResponseWriter, r *http.Request, err error) bool {
	var floor *errLauncherTooOld
	if errors.As(err, &floor) {
		d.writeLauncherTooOldError(w, r, floor)
		return true
	}
	var cs *errContentStorageRefused
	if errors.As(err, &cs) {
		writeErrorCode(w, http.StatusConflict, cs.code, d.translatorFor(r).Render(cs.message))
		return true
	}
	var unreadable *errStoredConfigUnreadable
	if errors.As(err, &unreadable) {
		writeInternalError(w, err)
		return true
	}
	return false
}

// isConfigGateRefusal reports whether err is one of the write-gate refusals,
// for the service functions that must hand it back to their handler unwrapped.
func isConfigGateRefusal(err error) bool {
	var floor *errLauncherTooOld
	var cs *errContentStorageRefused
	var unreadable *errStoredConfigUnreadable
	return errors.As(err, &floor) || errors.As(err, &cs) || errors.As(err, &unreadable)
}

// storedContentStorage is what the config currently in the store says about
// content storage: its type and the bundle it names. Read LENIENTLY, key by
// key, rather than through ParseNamespaceConfig: the gate must still see S3 in
// a stored config that fails some unrelated validation (a raw PUT is how such a
// config gets fixed), or the one-way rule would have a hole exactly there.
type storedContentStorage struct {
	external  bool
	typ       namespace.ContentStorageType
	bundleRef bundle.Ref
}

// loadStoredContentStorage reads the stored side of the gate. No stored config
// at all is a namespace being created: DB. A store that cannot be read is an
// error the caller must refuse on (errStoredConfigUnreadable) — never DB. And a
// stored file that is not even valid YAML (a hand edit of a server-mode
// namespace.yml) is scanned as text for its contentStorage block, because a
// syntax error anywhere in the file would otherwise read as "no block", i.e.
// DB, on exactly the write that is meant to repair it.
func (d *Daemon) loadStoredContentStorage(wsID, nsID string) (storedContentStorage, error) {
	out := storedContentStorage{typ: namespace.ContentStorageDB}
	if d.store == nil {
		return out, nil
	}
	raw, ok, err := d.store.LoadNamespaceConfig(wsID, nsID)
	if err != nil {
		return out, &errStoredConfigUnreadable{err: err}
	}
	if !ok {
		return out, nil
	}
	var cs struct {
		ContentStorage *namespace.ContentStorageProps `yaml:"contentStorage"`
		S3             *namespace.S3Config            `yaml:"s3"`
	}
	if yaml.Unmarshal([]byte(raw), &cs) == nil {
		out.external = cs.S3 != nil
		if cs.ContentStorage != nil {
			// An unknown type is not S3; it cannot be stored past the
			// validation anyway.
			if t, known := namespace.ParseContentStorageType(string(cs.ContentStorage.Type)); known {
				out.typ = t
			}
		}
	} else {
		out.typ = scanContentStorageType(raw)
		out.external = scanExternalS3Config(raw)
	}
	var ref struct {
		BundleRef bundle.Ref `yaml:"bundleRef"`
	}
	if yaml.Unmarshal([]byte(raw), &ref) == nil {
		out.bundleRef = ref.BundleRef
	}
	return out, nil
}

// scanExternalS3Config is conservative only for unreadable YAML: a top-level
// external block still protects its files while an unrelated syntax error is repaired.
func scanExternalS3Config(raw string) bool {
	for line := range strings.Lines(raw) {
		if strings.HasPrefix(stripYAMLComment(line), "s3:") {
			return true
		}
	}
	return false
}

// s3Word matches S3 as a word of its own, in any case.
var s3Word = regexp.MustCompile(`(?i)(^|[^a-z0-9_])s3([^a-z0-9_]|$)`)

// scanContentStorageType is the text fallback for a stored config that does
// not parse as YAML: the top-level `contentStorage:` block — the key's own
// line and the indented lines under it — read with comments stripped. Any S3
// there is S3. Deliberately biased that way: reading S3 where there is none
// only refuses a write that drops it, while missing it would let that write
// end the one-way choice.
func scanContentStorageType(raw string) namespace.ContentStorageType {
	inBlock := false
	for line := range strings.Lines(raw) {
		line = strings.TrimRight(line, "\r\n")
		code := stripYAMLComment(line)
		if strings.TrimSpace(code) == "" {
			continue
		}
		topLevel := line != "" && line[0] != ' ' && line[0] != '\t'
		if topLevel {
			key, rest, found := strings.Cut(code, ":")
			inBlock = found && strings.TrimSpace(key) == "contentStorage"
			if !inBlock {
				continue
			}
			code = rest
		} else if !inBlock {
			continue
		}
		if s3Word.MatchString(code) {
			return namespace.ContentStorageS3
		}
	}
	return namespace.ContentStorageDB
}

// stripYAMLComment drops a `#` comment: one at the start of the line or after
// whitespace (a `#` inside a word is not a comment in YAML).
func stripYAMLComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

// contentStorageRefusal is the content-storage half of the write gate. Two
// rules, both owner rulings:
//
//  1. S3 is ONE-WAY. A config that stores its content in S3 may not be
//     written back to DB — explicitly, or by a raw config that drops the
//     block (absent means DB). The files already in S3 are not in the
//     database, and nothing moves them.
//  2. S3 may be CHOSEN only where it works: the bundle the config names must
//     have ecos-content and the workspace a usable provider
//     (ContentStorageS3Availability), resolved OFFLINE like the launcher
//     floor. The question is asked when the choice is being made — a DB → S3
//     switch, or a different bundle under an S3 namespace — and not on an
//     edit that keeps both: refusing a rename because the workspace changed
//     under a namespace that cannot go back to DB would leave it uneditable,
//     and the generator reports that state anyway (ContentStorageProblem).
//     A bundle that is not on disk cannot be asked, so choosing S3 with one
//     is refused too: S3 cannot be undone, and an unverified choice is the
//     one way to be stuck with it.
func (d *Daemon) contentStorageRefusal(wsID string, stored storedContentStorage, next *namespace.Config) *errContentStorageRefused {
	if next == nil {
		return nil
	}
	transition := namespace.CheckContentStorageTransition(stored.typ, stored.bundleRef, next)
	if transition.Downgrade || namespace.ExternalS3Downgrade(stored.external, next) {
		return &errContentStorageRefused{
			code:    api.ErrCodeContentStorageDowngrade,
			message: msg.New("contentStorage.msg.downgrade"),
		}
	}
	if !transition.RequiresAvailability {
		return nil
	}
	avail, ok := d.contentStorageAvailability(wsID, next.BundleRef)
	if !ok {
		return &errContentStorageRefused{
			code:    api.ErrCodeContentStorageUnavailable,
			reason:  api.ContentStorageBundleUnresolved,
			message: namespace.ContentStorageUnavailableMessage(api.ContentStorageBundleUnresolved, next.BundleRef, ""),
		}
	}
	if !avail.Available {
		return &errContentStorageRefused{
			code:    api.ErrCodeContentStorageUnavailable,
			reason:  avail.Reason,
			message: namespace.ContentStorageUnavailableMessage(avail.Reason, next.BundleRef, avail.ProviderApp),
		}
	}
	return nil
}

// contentStorageAvailability resolves the bundle OFFLINE and answers the S3
// availability for it; ok is false when the bundle cannot be read from disk.
func (d *Daemon) contentStorageAvailability(wsID string, ref bundle.Ref) (namespace.ContentStorageAvailability, bool) {
	if ref.IsEmpty() {
		return namespace.ContentStorageAvailability{}, false
	}
	res, err := d.resolveBundleOffline(wsID, ref)
	if err != nil || res == nil || res.Bundle == nil {
		return namespace.ContentStorageAvailability{}, false
	}
	ws := res.Workspace
	if ws == nil {
		ws = d.workspaceConfigFor(wsID)
	}
	return namespace.ContentStorageS3Availability(res.Bundle, ws), true
}

// workspaceConfigFor is the live workspace config when wsID is the active
// workspace, nil otherwise.
func (d *Daemon) workspaceConfigFor(wsID string) *bundle.WorkspaceConfig {
	act := d.active()
	if act.workspaceID != wsID {
		return nil
	}
	return act.workspaceConfig
}

// handleContentStorageOptions answers GET /content-storage/options: which
// content storage types a namespace on the given bundle may choose. Resolved
// offline, like the write gate, so the dialog shows exactly what saving will
// accept. With no bundle in the query, the active namespace's is asked.
func (d *Daemon) handleContentStorageOptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	wsID := q.Get("workspaceId")
	if wsID == "" {
		wsID = d.activeWorkspaceID()
	} else if !validateID(wsID) {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	ref := bundle.Ref{Repo: q.Get("bundleRepo"), Key: q.Get("bundleKey")}
	if ref.IsEmpty() {
		if act := d.active(); act.nsConfig != nil && act.workspaceID == wsID {
			ref = act.nsConfig.BundleRef
		}
	}
	opt := api.ContentStorageOptionDto{Reason: api.ContentStorageBundleUnresolved}
	if avail, ok := d.contentStorageAvailability(wsID, ref); ok {
		opt = api.ContentStorageOptionDto{
			Available:     avail.Available,
			Reason:        avail.Reason,
			ProviderApp:   avail.ProviderApp,
			ProviderTitle: avail.ProviderTitle,
		}
	} else if s3 := d.workspaceConfigFor(wsID).ContentStorageS3(); s3 != nil {
		opt.ProviderApp = s3.App
		opt.ProviderTitle = s3.DisplayName()
	}
	writeJSON(w, api.ContentStorageOptionsDto{S3: opt})
}

// handleContentStorageProvision answers POST /namespace/content-storage/provision:
// run the connection of the active namespace now (the Retry button).
func (d *Daemon) handleContentStorageProvision(w http.ResponseWriter, r *http.Request) {
	act := d.active()
	if act.runtime == nil || act.nsConfig == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	if !act.nsConfig.UsesS3ContentStorage() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeContentStorageNotS3,
			d.translatorFor(r).T("contentStorage.msg.notS3"))
		return
	}
	d.contentStorageProv().Retry(act.nsConfig.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.ActionResultDto{Success: true, Message: "content storage connection requested"})
}

// contentStorageDto is NamespaceDto.ContentStorage: nil for a DB namespace,
// otherwise the provider, the generation's problem and the provisioner's
// state, rendered for this reader.
func (d *Daemon) contentStorageDto(t *i18n.Translator, act activeNamespace) *api.ContentStorageDto {
	if act.nsConfig == nil || !act.nsConfig.UsesS3ContentStorage() {
		return nil
	}
	dto := &api.ContentStorageDto{Type: string(namespace.ContentStorageS3)}
	if s3 := act.workspaceConfig.ContentStorageS3(); act.nsConfig.S3 == nil && s3 != nil {
		dto.ProviderApp = s3.App
		dto.ProviderTitle = s3.DisplayName()
	}
	if p := act.contentStorageProblem; p != nil {
		dto.Problem = p.Reason
		if dto.ProviderApp == "" {
			dto.ProviderApp = p.ProviderApp
			dto.ProviderTitle = p.ProviderApp
		}
	}
	st := d.contentStorageProv().Status(act.nsConfig.ID)
	dto.State = st.State
	if !st.ErrorMsg.Empty() {
		dto.Error = t.Render(st.ErrorMsg)
	}
	if !st.At.IsZero() {
		dto.At = st.At.UTC().Format(time.RFC3339)
	}
	return dto
}

// switchedToS3 reports whether a write moved a namespace from DB to S3 — the
// one transition that re-attaches the provider.
func switchedToS3(before namespace.ContentStorageType, after *namespace.Config) bool {
	return before != namespace.ContentStorageS3 && after.ContentStorageType() == namespace.ContentStorageS3
}

// requiredAppsFor is namespace.RequiredApps against the workspace config the
// namespace is generated with: the live one when wsID is active, the offline
// resolve's otherwise.
func (d *Daemon) requiredAppsFor(wsID string, cfg *namespace.Config) []string {
	ws := d.workspaceConfigFor(wsID)
	if ws == nil {
		if res, err := d.resolveBundleOffline(wsID, cfg.BundleRef); err == nil && res != nil {
			ws = res.Workspace
		}
	}
	return namespace.RequiredApps(cfg, ws)
}

// reattachRequiredApps clears the operator's detach of every app the config
// now REQUIRES, once, as part of the explicit operator action that made it
// required (today: switching content storage DB → S3, which needs the
// provider). The default workspace template detaches the provider, so without
// this the switch would leave ecos-content waiting in DEPS_WAITING behind an
// app nothing will ever start.
//
// It is NOT a load or reload rule — those never clear a detach (owner ruling:
// clearing a detach starts nothing). An operator who stops the provider on an
// S3 namespace afterwards is honored: content then waits for it.
//
// The active namespace is re-attached through its runtime (ReattachApp, which
// also starts the app on a live loop); any other namespace through its
// persisted state, which is what its next load reads. A namespace with no
// persisted state has never started, and its first load subtracts required
// apps from the template's detach set by itself.
func (d *Daemon) reattachRequiredApps(wsID, nsID string, cfg *namespace.Config) {
	apps := d.requiredAppsFor(wsID, cfg)
	if len(apps) == 0 {
		return
	}
	act := d.active()
	if act.runtime != nil && act.nsConfig != nil && act.workspaceID == wsID && act.nsConfig.ID == nsID {
		for _, app := range apps {
			if act.runtime.ReattachApp(app) {
				slog.Info("Re-attached an app the namespace now requires", "ns", nsID, "app", app) //nolint:gosec // G706: validated namespace id, app id from the workspace config
			}
		}
		return
	}
	st := loadNsStateFromStore(d.store, wsID, nsID)
	if st == nil {
		return
	}
	kept := slices.DeleteFunc(slices.Clone(st.ManualStoppedApps), func(name string) bool {
		return slices.Contains(apps, name)
	})
	if len(kept) == len(st.ManualStoppedApps) {
		return
	}
	st.ManualStoppedApps = kept
	data, err := json.Marshal(st)
	if err == nil {
		err = d.store.SaveNamespaceState(wsID, nsID, string(st.Status), string(data))
	}
	if err != nil {
		slog.Warn("Could not re-attach the apps the namespace now requires", "ns", nsID, "apps", apps, "err", err) //nolint:gosec // G706: validated namespace id, app ids from the workspace config
		return
	}
	slog.Info("Re-attached the apps the namespace now requires", "ns", nsID, "apps", apps) //nolint:gosec // G706: validated namespace id, app ids from the workspace config
}

// withoutRequiredApps removes from a template's detach list every app the
// namespace's own configuration requires. A NEW S3 namespace must start its
// provider with the namespace, whatever the template holds back by default.
func withoutRequiredApps(detached []string, cfg *namespace.Config, ws *bundle.WorkspaceConfig) []string {
	required := namespace.RequiredApps(cfg, ws)
	if len(required) == 0 || len(detached) == 0 {
		return detached
	}
	return slices.DeleteFunc(slices.Clone(detached), func(name string) bool {
		return slices.Contains(required, name)
	})
}
