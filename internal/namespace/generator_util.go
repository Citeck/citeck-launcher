package namespace

// Generator helpers (template-var resolution, license merging, YAML helpers,
// content hashing) — split out of generator.go. Pure code motion; the
// orchestrating Generate stays in generator.go.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/appfiles"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"gopkg.in/yaml.v3"
)

// UtilsImage returns the launcher-utils image from config (supports env override).
var UtilsImage = config.UtilsImage()

func bundleImageOr(ctx *NsGenContext, name, fallback string) string {
	if app, ok := ctx.Bundle.Applications[name]; ok && app.Image != "" {
		return app.Image
	}
	return fallback
}

func loadAppFiles(ctx *NsGenContext) {
	files, err := appfiles.GetFiles()
	if err != nil {
		return
	}
	maps.Copy(ctx.Files, files)
}

// injectLicensesAndBundleKey writes eapps license + bundle-key entries into the
// webapp and external cloud-config maps. It is a no-op when mergedLicenses is empty.
func injectLicensesAndBundleKey(mergedLicenses []bundle.LicenseInstance, bun *bundle.Def, webappCC, extCC map[string]any) {
	if len(mergedLicenses) == 0 {
		return
	}
	var licenseStrings []string
	for _, lic := range mergedLicenses {
		if data, err := json.Marshal(lic); err == nil {
			licenseStrings = append(licenseStrings, string(data))
		}
	}
	webappCC["ecos.webapp.license.instances"] = licenseStrings
	bundleKey := bun.Key.Version
	webappCC["citeck.bundle.key"] = bundleKey
	extCC["citeck.bundle.key"] = bundleKey
	if bun.Content != nil {
		bundleContent, _ := json.Marshal(bun.Content)
		webappCC["citeck.bundle.content"] = string(bundleContent)
		extCC["citeck.bundle.content"] = string(bundleContent)
	}
}

// mergeLicenses returns the workspace-declared licenses merged with user-added
// (extra) licenses. Extras take precedence by ID. The returned slice is sorted
// by descending Priority — same order license.Service.List() exposes to the UI,
// so the highest-priority license is always the head of the list passed to
// webapps via `ecos.webapp.license.instances`.
func mergeLicenses(workspace, extras []bundle.LicenseInstance) []bundle.LicenseInstance {
	if len(workspace) == 0 && len(extras) == 0 {
		return nil
	}
	byID := make(map[string]bundle.LicenseInstance, len(workspace)+len(extras))
	order := make([]string, 0, len(workspace)+len(extras))
	for _, lic := range workspace {
		if _, dup := byID[lic.ID]; !dup {
			order = append(order, lic.ID)
		}
		byID[lic.ID] = lic
	}
	for _, lic := range extras {
		if _, dup := byID[lic.ID]; !dup {
			order = append(order, lic.ID)
		}
		// Extras win on collision.
		byID[lic.ID] = lic
	}
	out := make([]bundle.LicenseInstance, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Priority > out[j].Priority
	})
	return out
}

// deepMergeMaps recursively merges src into dst. For keys present in both maps,
// if both values are map[string]any, they are merged recursively; otherwise src wins.
func deepMergeMaps(dst, src map[string]any) {
	for k, srcVal := range src {
		if dstVal, ok := dst[k]; ok {
			dstMap, dstIsMap := dstVal.(map[string]any)
			srcMap, srcIsMap := srcVal.(map[string]any)
			if dstIsMap && srcIsMap {
				deepMergeMaps(dstMap, srcMap)
				continue
			}
		}
		dst[k] = srcVal
	}
}

// rewriteDataSourceURLForLocalhost rewrites a datasource URL to use localhost with published ports.
func rewriteDataSourceURLForLocalhost(url, _ string) string {
	if strings.HasPrefix(url, "jdbc:postgresql://") {
		// Rewrite postgres host:port to localhost with published port.
		url = strings.Replace(url, fmt.Sprintf("%s:%d", PGHost, PGPort), "localhost:14523", 1)
	} else if strings.HasPrefix(url, "mongodb://") {
		// Rewrite mongo host:port to localhost with published port.
		url = strings.Replace(url, fmt.Sprintf("%s:%d", MongoHost, MongoPort), "localhost:27017", 1)
	}
	return url
}

// flatMapToYAML converts a flat dot-separated key map into nested YAML.
func flatMapToYAML(m map[string]any) string {
	// Build nested structure from flat keys
	root := make(map[string]any)
	for k, v := range m {
		parts := strings.Split(k, ".")
		current := root
		for i, p := range parts {
			if i == len(parts)-1 { //nolint:nestif // nested map building
				current[p] = v
			} else {
				if next, ok := current[p]; ok {
					if nextMap, ok := next.(map[string]any); ok {
						current = nextMap
					} else {
						slog.Warn("flatMapToYAML: key conflict, dropping", "key", k)
						break
					}
				} else {
					next := make(map[string]any)
					current[p] = next
					current = next
				}
			}
		}
	}
	// yaml.Marshal hardcodes a 4-space indent; drive an encoder for the 2-space
	// indent the editor (and the rest of our generated YAML) uses.
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		_ = enc.Close()
		return ""
	}
	_ = enc.Close()
	return buf.String()
}

// resolveTemplateVars replaces ${VAR} placeholders in datasource URLs.
// resolveTemplateVarsWithContext resolves template variables including
// config- and secrets-dependent ones. Used by applyWebappDefaults when
// populating environment variables from workspace defaults.
//
// ${KK_ADMIN_USER} and ${KK_ADMIN_PASSWORD} resolve to the "citeck" service
// account credentials. Webapps use these to authenticate to Keycloak for
// admin operations (user management, role checks). Using the SA instead
// of admin decouples webapp auth from the user-facing admin password.
func resolveTemplateVarsWithContext(s string, ctx *NsGenContext) string {
	kkEnabled := "false"
	if ctx != nil && ctx.Config != nil && ctx.Config.Authentication.Type == AuthKeycloak {
		kkEnabled = "true"
	}
	// KK_ADMIN_URL is always set (Kotlin NsGenContext.VARS)
	kkAdminURL := fmt.Sprintf("http://%s:8080", KKHost)
	// Use the dedicated "citeck" SA for webapp→Keycloak integration.
	// This avoids coupling webapps to the user-facing admin password.
	kkAdminUser := CiteckSAUser
	kkAdminPassword := "admin" // fallback for tests
	if ctx != nil && ctx.Secrets.CiteckSA != "" {
		kkAdminPassword = ctx.Secrets.CiteckSA
	}

	// Platform-managed secrets + the public base URL, so a config-driven service
	// (additionalApps) integrates with ECOS auth/messaging without hardcoding them
	// in the workspace config. All resolve from the generation context; empty when
	// ctx is nil (unit tests / pre-secret generate).
	var jwtSecret, oidcSecret, rmqPassword, webURL, adminPassword string
	if ctx != nil {
		jwtSecret = ctx.Secrets.JWT
		oidcSecret = ctx.Secrets.OIDC
		rmqPassword = ctx.Secrets.CiteckSA
		webURL = ctx.ProxyBaseURL()
		adminPassword = ctx.Secrets.AdminPasswordOrDefault()
	}

	s = strings.ReplaceAll(s, "${KK_ENABLED}", kkEnabled)
	s = strings.ReplaceAll(s, "${KK_ADMIN_URL}", kkAdminURL)
	s = strings.ReplaceAll(s, "${KK_ADMIN_USER}", kkAdminUser)
	s = strings.ReplaceAll(s, "${KK_ADMIN_PASSWORD}", kkAdminPassword)
	s = strings.ReplaceAll(s, "${KK_HOST}", KKHost)
	s = strings.ReplaceAll(s, "${JWT_SECRET}", jwtSecret)
	s = strings.ReplaceAll(s, "${OIDC_SECRET}", oidcSecret)
	s = strings.ReplaceAll(s, "${WEB_URL}", webURL)
	s = strings.ReplaceAll(s, "${RMQ_USER}", CiteckSAUser)
	s = strings.ReplaceAll(s, "${RMQ_PASSWORD}", rmqPassword)
	s = strings.ReplaceAll(s, "${ADMIN_PASSWORD}", adminPassword)
	return resolveTemplateVars(s)
}

func resolveTemplateVars(s string) string {
	replacements := map[string]string{
		"${PG_HOST}":         PGHost,
		"${PG_PORT}":         fmt.Sprintf("%d", PGPort),
		"${MONGO_HOST}":      MongoHost,
		"${MONGO_PORT}":      fmt.Sprintf("%d", MongoPort),
		"${ZK_HOST}":         ZKHost,
		"${ZK_PORT}":         fmt.Sprintf("%d", ZKPort),
		"${RMQ_HOST}":        RMQHost,
		"${RMQ_PORT}":        fmt.Sprintf("%d", RMQPort),
		"${MAILHOG_HOST}":    MailhogHost,
		"${ONLYOFFICE_HOST}": OnlyofficeHost,
	}
	for k, v := range replacements {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// extractDBName extracts the database name from a JDBC or MongoDB URL.
// jdbc:postgresql://host:port/dbname?params -> dbname
func extractDBName(url string) string {
	idx := strings.LastIndex(url, "/")
	if idx < 0 {
		return ""
	}
	name := url[idx+1:]
	// Strip query parameters
	if qIdx := strings.IndexByte(name, '?'); qIdx >= 0 {
		name = name[:qIdx]
	}
	return name
}

// computeVolumesContentHash returns a short, human-friendly digest of the
// content of every bind-mount source file referenced by `app` (own volumes
// + init container volumes). Feeds into ApplicationDef.GetHashInput so the
// deployment hash changes when any bind-mounted file content changes —
// prompting the runtime to recreate the container with the fresh content.
//
// Algorithm (Kotlin parity — NsRuntimeFiles.getPathsContentHash):
//  1. Per file: SHA-256 hex digest.
//  2. Aggregate: feed each per-file digest into a 64-bit non-crypto hash
//     stream (xxhash), walking files in sorted-path order so the result
//     is deterministic across runs and map iteration order.
//  3. Render: encode the 8-byte sum as URL-safe base64 with padding
//     stripped — ~11 chars instead of 64-char hex.
//
// We picked the small-output aggregator on purpose so the hash doesn't
// visually swamp logs and Docker labels; xxhash's collision odds at our
// scale (<10⁴ distinct file sets per namespace) are negligible.
//
// Only `./...` host paths are hashed. Named volumes (`pgdata:/var/lib/...`)
// and absolute host paths (`/etc/foo:/inside`) aren't touched — those are
// either Docker-managed state or out-of-scope of the embedded file set.
func computeVolumesContentHash(app *appdef.ApplicationDef, files map[string][]byte) string {
	keys := collectFileKeysFromVolumes(app.Volumes)
	for _, ic := range app.InitContainers {
		for _, k := range collectFileKeysFromVolumes(ic.Volumes) {
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys) // stable order — map iteration isn't
	agg := xxhash.New()
	for _, k := range keys {
		content, ok := files[k]
		if !ok {
			continue
		}
		// Per-file digest folds the path into SHA-256 so a rename (same
		// content, different key) still flips the aggregate hash. Kotlin's
		// runtimeFilesHash was keyed by path and the TreeMap ordering did
		// this implicitly; in Go we make it explicit.
		perFile := sha256.New()
		perFile.Write([]byte(k))
		perFile.Write([]byte{0})
		perFile.Write(content)
		_, _ = agg.WriteString(hex.EncodeToString(perFile.Sum(nil)))
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], agg.Sum64())
	return base64.RawURLEncoding.EncodeToString(buf[:])
}

// collectFileKeysFromVolumes extracts the ctx.Files keys (e.g.
// "postgres/postgresql.conf") from a list of Docker volume specs. Strips
// the leading "./" and everything from the first colon onwards. Skips
// specs that don't reference a bind-mounted file (named volumes, abs
// host paths).
func collectFileKeysFromVolumes(vols []string) []string {
	var keys []string
	for _, v := range vols {
		// "./postgres/postgresql.conf:/etc/postgresql/postgresql.conf[:ro]"
		if !strings.HasPrefix(v, "./") {
			continue
		}
		host := strings.TrimPrefix(v, "./")
		if idx := strings.Index(host, ":"); idx >= 0 {
			host = host[:idx]
		}
		if host != "" {
			keys = append(keys, host)
		}
	}
	return keys
}
