package cli

import (
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/citeck/citeck-launcher/internal/systemsecrets"
)

// `citeck diagnose --secrets` — one-shot consistency audit of the three homes
// a system secret can live in (plain launcher_state `_sys_*` key, legacy
// SecretService row, pre-Store plain file). The daemon's resolver
// (systemsecrets.Get) migrates lower-priority homes forward on every start,
// so in a healthy install each id has exactly one copy — in launcher_state.
// Leftovers are how the silently-reverting-rotation class of bugs hid: a
// stale lower-priority copy is invisible while the effective value exists,
// then "wins" the moment the state key is cleared. This audit makes those
// shadows visible BEFORE they bite.
//
// Strictly read-only (systemsecrets.Inspect — never migrates, deletes, or
// generates) and offline: it opens the local FileStore directly, exactly like
// the install wizard, so it works whether or not the daemon is running.

// runSecretsAudit opens the local store and builds the audit check list.
func runSecretsAudit() []diagnoseCheck {
	store, err := storage.NewFileStore(config.ConfDir(), filepath.Join(config.DataDir(), "runtime"))
	if err != nil {
		return []diagnoseCheck{{
			Name:    "secrets:store",
			Status:  "error",
			Message: t("diagnose.secrets.storeError", "err", err.Error()),
		}}
	}
	svc, svcErr := systemsecrets.OpenDefaultUnlocked(store)
	if svcErr != nil {
		svc = nil // degrade to state + plain-file inspection only
	}
	return buildSecretsChecks(store, svc)
}

// buildSecretsChecks audits every known system-secret id against an already
// opened store. Split from runSecretsAudit so tests can feed a fixture store
// without touching the real CITECK_HOME.
func buildSecretsChecks(store storage.Store, svc *storage.SecretService) []diagnoseCheck {
	ids := systemsecrets.KnownIDs()
	checks := make([]diagnoseCheck, 0, len(ids)+1)

	// A custom (non-default) master password keeps the SecretService locked
	// for offline CLI access — legacy rows are then invisible to this audit
	// (treated as absent), which the user must know to interpret the result.
	if svc != nil && svc.IsEncrypted() && svc.IsLocked() {
		checks = append(checks, diagnoseCheck{
			Name:    "secrets:storage",
			Status:  "warning",
			Message: t("diagnose.secrets.locked"),
		})
	}

	for _, id := range ids {
		checks = append(checks, auditOneSecret(store, svc, id)...)
	}
	return checks
}

// auditOneSecret renders one id's homes into checks: the effective location
// first (what systemsecrets.Get would return), then one entry per shadowed
// lower-priority copy — identical copies are a WARN (the daemon migrates and
// deletes them on next start), diverging copies are an ERROR with a cleanup
// recommendation. This is a consistency audit: it compares values INTERNALLY
// (effective vs shadow) but never prints a secret value — not even masked. It
// reports only WHERE each copy lives and whether copies agree. A forgotten
// admin password is recovered by rotating it (`citeck setup admin-password`),
// not by reading it back here.
func auditOneSecret(store storage.Store, svc *storage.SecretService, id string) []diagnoseCheck {
	locs := systemsecrets.Inspect(store, svc, id)
	if len(locs) == 0 {
		return []diagnoseCheck{{
			Name:    "secret:" + id,
			Status:  "warning",
			Message: t("diagnose.secrets.missing", "id", id),
		}}
	}

	effective := locs[0]
	checks := make([]diagnoseCheck, 0, len(locs))
	checks = append(checks, diagnoseCheck{
		Name:   "secret:" + id,
		Status: "ok",
		Message: t("diagnose.secrets.effective",
			"id", id,
			"source", string(effective.Source),
			"detail", effective.Detail),
	})

	for _, loc := range locs[1:] {
		if loc.Value == effective.Value {
			checks = append(checks, diagnoseCheck{
				Name:   "secret:" + id,
				Status: "warning",
				Message: t("diagnose.secrets.duplicate",
					"id", id,
					"source", string(loc.Source),
					"detail", loc.Detail),
			})
			continue
		}
		checks = append(checks, diagnoseCheck{
			Name:   "secret:" + id,
			Status: "error",
			Message: t("diagnose.secrets.stale",
				"id", id,
				"source", string(loc.Source),
				"detail", loc.Detail),
			FixHint: t("diagnose.secrets.hint.stale"),
		})
	}
	return checks
}
