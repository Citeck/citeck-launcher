// Secret redaction for dump-system-info.
//
// The diagnostics archive collects container inspects, daemon/container logs
// and config files — all of which can contain plaintext secrets (the launcher
// injects passwords/tokens as container env vars, and logs init actions like
// `rabbitmqctl add_user citeck <pass>`). We harvest the actual secret VALUES up
// front (from container env + the secret store) and mask every occurrence of
// those exact strings in every artifact written to the archive.

package cli

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// redactPlaceholder replaces every secret occurrence in dump artifacts.
const redactPlaceholder = "***REDACTED***"

// minSecretLen guards against masking short, non-secret values (e.g. "true",
// "admin", ports) whose blanket replacement would corrupt the diagnostics
// without protecting anything. Server-mode secrets are long random strings.
const minSecretLen = 6

// secretEnvKeyRe matches env var KEYS whose VALUE should be treated as a secret.
// It matches a secret word as a full `_`-delimited segment (so PASS matches
// DEFAULT_PASS but not BYPASS), plus the common compound *_KEY forms. Tuned to
// catch the launcher's secret env vars (RABBITMQ_DEFAULT_PASS,
// *_RABBITMQ_PASSWORD, *_JWT_SECRET, *_CLIENT_SECRET, KC_BOOTSTRAP_ADMIN_PASSWORD,
// RMQ_MONITOR_PASSWORD, …) without sweeping up PGP_KEY_ID, PUBLIC_KEY_PATH, etc.
var secretEnvKeyRe = regexp.MustCompile(
	`(?i)(^|_)(pass|passwd|password|secret|token|credential|credentials)(_|$)` +
		`|(private|access|api|secret|encryption)[_-]?key`,
)

// secretRedactor masks known secret VALUES wherever they appear across all dump
// artifacts. Values are harvested once up front, then redact() is applied to
// every entry written to the archive.
type secretRedactor struct {
	values []string // unique; finalize() sorts longest-first
}

func newSecretRedactor() *secretRedactor { return &secretRedactor{} }

// count reports how many secret values were harvested (used to warn when
// redaction found nothing despite containers being present).
func (r *secretRedactor) count() int { return len(r.values) }

// addValue records a secret value to mask, skipping trivial values that would
// cause noisy over-redaction.
func (r *secretRedactor) addValue(v string) {
	v = strings.TrimSpace(v)
	if !isLikelySecret(v) {
		return
	}
	if slices.Contains(r.values, v) {
		return
	}
	r.values = append(r.values, v)
}

// harvestEnv scans container env lines ("KEY=VALUE") and records the VALUE of
// any secret-bearing KEY.
func (r *secretRedactor) harvestEnv(env []string) {
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && secretEnvKeyRe.MatchString(k) {
			r.addValue(v)
		}
	}
}

// finalize sorts harvested values longest-first so a secret that is a substring
// of another doesn't leave the longer one partially exposed.
func (r *secretRedactor) finalize() {
	sort.Slice(r.values, func(i, j int) bool {
		return len(r.values[i]) > len(r.values[j])
	})
}

// redact replaces every harvested secret value in data with the placeholder.
func (r *secretRedactor) redact(data []byte) []byte {
	if len(r.values) == 0 || len(data) == 0 {
		return data
	}
	s := string(data)
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, redactPlaceholder)
	}
	return []byte(s)
}

// buildDumpRedactor harvests the secret VALUES to mask from two sources:
//  1. running launcher containers' env — the authoritative place the launcher
//     injects passwords/tokens; works even when the secret store is locked,
//  2. the secret store itself — best-effort, catches secrets not present in any
//     running container's env; skipped when encrypted-and-locked.
//
// Both are best-effort: a failure in either leaves the other's values in place.
// Harvesting up front lets every archive entry be stream-redacted on write.
func buildDumpRedactor(ctx context.Context) *secretRedactor {
	r := newSecretRedactor()
	harvestContainerEnvSecrets(ctx, r)
	harvestStoreSecrets(r)
	harvestAPIToken(r)
	r.finalize()
	return r
}

// harvestAPIToken records the api_auth token (daemon.yml api_auth.token or
// the daemon-generated conf/api-token file) so the verbatim daemon.yml copy
// in the dump — and any other artifact echoing the token — gets it masked.
// Best-effort: silently skipped when neither source is readable.
func harvestAPIToken(r *secretRedactor) {
	cfg, err := config.LoadDaemonConfig()
	if err != nil {
		return
	}
	if token, tokenErr := config.LoadAPIToken(cfg); tokenErr == nil {
		r.addValue(token)
	}
}

// harvestContainerEnvSecrets records secret-looking env values from every
// launcher container.
func harvestContainerEnvSecrets(ctx context.Context, r *secretRedactor) {
	cli, err := docker.NewClient("", "dump")
	if err != nil {
		return
	}
	defer cli.Close()

	cctx, cancel := context.WithTimeout(ctx, dockerCmdTimeout)
	defer cancel()
	list, err := cli.ListAllLauncherContainers(cctx)
	if err != nil {
		return
	}
	for _, c := range list {
		if c.ID == "" {
			continue
		}
		insp, err := cli.InspectContainer(cctx, c.ID)
		if err != nil || insp.Config == nil {
			continue
		}
		r.harvestEnv(insp.Config.Env)
	}
}

// harvestStoreSecrets records secret values straight from the launcher's secret
// store (system + user secrets). Skipped silently when the store is encrypted
// and locked — the env harvest still covers everything actually injected.
func harvestStoreSecrets(r *secretRedactor) {
	var store storage.Store
	var err error
	if config.IsDesktopMode() {
		store, err = storage.NewSQLiteStore(config.HomeDir())
	} else {
		store, err = storage.NewFileStore(config.ConfDir(), filepath.Join(config.DataDir(), "runtime"))
	}
	if err != nil {
		return
	}
	defer store.Close()

	svc, err := storage.NewSecretService(store)
	if err != nil || svc.IsLocked() {
		return
	}
	metas, err := svc.ListSecrets()
	if err != nil {
		return
	}
	for _, m := range metas {
		sec, secErr := svc.GetSecret(m.ID)
		if secErr != nil || sec == nil {
			continue
		}
		r.addValue(sec.Value)
	}
}

// isLikelySecret filters values that are too short or are common non-secret
// tokens, plus anything containing whitespace (a secret value never does, but a
// log line or sentence might) so we don't mangle prose.
func isLikelySecret(v string) bool {
	if len(v) < minSecretLen {
		return false
	}
	if strings.ContainsAny(v, " \t\r\n") {
		return false
	}
	switch strings.ToLower(v) {
	case "true", "false", "default", "disabled", "enabled", "localhost":
		return false
	}
	return true
}
