package namespace

// Generator orchestration. Per-service-family generators live in sibling
// files: generator_infra.go (mailpit / mongo / pgadmin / postgres /
// zookeeper / rabbitmq), generator_keycloak.go, generator_proxy.go,
// generator_webapp.go (bundle webapps + alfresco / observer / stt-sidecar /
// onlyoffice), generator_util.go (template-var resolution, license merging,
// YAML helpers, content hashing).

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// GenResp is the result of namespace generation.
type GenResp struct {
	Applications          []appdef.ApplicationDef
	BaselineApplications  []appdef.ApplicationDef // patch-free baseline for the editor gutter + DiffAppDef
	Files                 map[string][]byte
	BaselineFiles         map[string][]byte         // generated file content BEFORE user-edit merge (baseline source for the editor + WriteEditedFile template)
	CloudConfig           map[string]map[string]any // per-app ext cloud config for CloudConfigServer
	DependsOnDetachedApps map[string]bool           // apps whose reattachment triggers regeneration
	CustomLinks           []bundle.WorkspaceLink    // workspace-config custom quick links (with dependsOn gating)
	DependencyUpgrades    []DependencyUpgrade       // candidates held back by a pin (registry order)
	Dependencies          map[deps.ID]DependencyGen // effective vs candidate image per dependency
}

// GenerateOpts holds optional parameters for namespace generation.
type GenerateOpts struct {
	DetachedApps map[string]bool // manually stopped apps excluded from dependency graph
	SecretReader SecretReader    // resolves "secret:" references in config (nil = no resolution)
	// ExtraLicenses are user-added enterprise licenses (stored encrypted via the
	// license Service). They are merged with workspace.licenses and emitted into
	// the eapps `ecos.webapp.license.instances` cloud-config key. Entries here
	// take precedence over workspace entries with the same ID; after dedupe the
	// merged list is sorted by descending Priority (mirrors Service.List()).
	ExtraLicenses []bundle.LicenseInstance
	// EditedFileEdits maps canonical ctx.Files keys ("<app>/<rel>", no leading
	// "./") to per-file edit deltas. After generation, each delta is merged onto
	// its template so both the on-disk materialized file AND VolumesContentHash
	// reflect the merged result. DiskContent supplies the YAML comment source /
	// textual conflict fallback.
	EditedFileEdits map[string]FileEdit
	DiskContent     map[string][]byte
	// EditedAppPatches maps app name → the stored shallow def patch. Applied at
	// the tail of Generate to produce the effective Applications; the patch-free
	// set is returned as BaselineApplications. Symmetric to EditedFileEdits.
	EditedAppPatches map[string]json.RawMessage
	// DependencyStates: per infra dependency, the image its data last ran on
	// and which generation of its data volume that is
	// (Runtime.DependencyStates). Nil for a namespace with no data yet, which
	// generates generation 1 — the volume every namespace has always used.
	DependencyStates map[deps.ID]deps.DependencyState
}

// Generate creates container definitions from a namespace config, bundle, and workspace config.
// Returns an error if a fatal generation step fails (e.g. rendering the Keycloak
// init script); callers should abort the reload/start on error rather than
// deploy a half-configured namespace.
func Generate(cfg *Config, bun *bundle.Def, wsCfg *bundle.WorkspaceConfig, secrets SystemSecrets, opts ...GenerateOpts) (*GenResp, error) {
	ctx := NewNsGenContext(cfg, bun)
	ctx.WorkspaceConfig = wsCfg
	ctx.Secrets = secrets
	if len(opts) > 0 {
		if opts[0].DetachedApps != nil {
			ctx.DetachedApps = opts[0].DetachedApps
		}
		if opts[0].SecretReader != nil {
			ctx.SecretReader = opts[0].SecretReader
		}
		ctx.ExtraLicenses = opts[0].ExtraLicenses
		ctx.EditedFileEdits = opts[0].EditedFileEdits
		ctx.DiskContent = opts[0].DiskContent
		ctx.EditedAppPatches = opts[0].EditedAppPatches
		ctx.DependencyStates = opts[0].DependencyStates
	}

	// Load embedded appfiles
	loadAppFiles(ctx)

	// Generate infrastructure services
	generateMailpit(ctx)
	generateMongoDB(ctx)
	generatePgAdmin(ctx)
	generatePostgres(ctx)
	generateZookeeper(ctx)
	generateRabbitMQ(ctx)
	if err := generateKeycloak(ctx); err != nil {
		return nil, fmt.Errorf("generate keycloak: %w", err)
	}
	generateAlfresco(ctx)
	generateObserver(ctx)

	// Generate webapps from bundle — only for apps declared in workspace config
	// (matching Kotlin: context.workspaceConfig.webappsById.contains(app.key))
	// Sort names for deterministic port assignment via NextPort().
	wsWebapps := make(map[string]bool)
	if wsCfg != nil {
		for _, w := range wsCfg.Webapps {
			wsWebapps[w.ID] = true
		}
	}
	webappNames := make([]string, 0, len(bun.Applications))
	for name := range bun.Applications {
		if len(wsWebapps) > 0 && !wsWebapps[name] {
			continue
		}
		webappNames = append(webappNames, name)
	}
	sort.Strings(webappNames)
	for _, name := range webappNames {
		// Collision guard, same rule as generateAdditionalApps: an infra/core app
		// cannot be redefined as a webapp — skip it, never overwrite, and say so
		// loudly. See isBuiltInApp for the two directions it covers.
		if isBuiltInApp(ctx, name) {
			slog.Error("bundle application collides with a built-in app; skipping it as a webapp to avoid overwriting the built-in definition",
				"name", name)
			continue
		}
		generateWebapp(name, ctx)
	}

	// STT sidecar (speech-to-text proxy for AI websocket traffic) — must run
	// AFTER the AI webapp is generated because it injects an env var + dep
	// onto the AI app, and before generateProxy so a future AI_TARGET wiring
	// in the proxy can read the resolved STT port from the apps map.
	generateSttSidecar(ctx)

	// Custom containers added by configuration alone (no dedicated generator).
	generateAdditionalApps(ctx)

	// Generate proxy (depends on gateway, onlyoffice)
	generateProxy(ctx)
	generateOnlyOffice(ctx)

	// All generators have run — every app name is now known. Drop any app whose
	// dependsOn points at an app that wasn't generated (transitively); see
	// pruneAppsWithMissingDeps.
	pruneAppsWithMissingDeps(ctx)

	// Server mode: only proxy publishes ports — all other apps are internal to Docker network.
	// Desktop mode: all ports published for local debugging (CloudConfigServer, direct DB access, etc.)
	if !config.IsDesktopMode() {
		for _, b := range ctx.Applications {
			if b.Name != appdef.AppProxy {
				b.Ports = nil
			}
		}
	}

	// Every app gets the export directory (see attachExportDir).
	for _, b := range ctx.Applications {
		attachExportDir(b)
	}

	// Build baseline (patch-free) applications.
	baselineApps := make([]appdef.ApplicationDef, 0, len(ctx.Applications))
	for _, b := range ctx.Applications {
		baselineApps = append(baselineApps, b.Build())
	}

	// Effective applications: overlay each stored patch onto its baseline.
	// ApplyAppDefPatch is the same shallow merge the runtime used; a nil/failed
	// patch leaves the baseline as the effective def.
	apps := make([]appdef.ApplicationDef, len(baselineApps))
	for i, base := range baselineApps {
		apps[i] = base
		if p := ctx.EditedAppPatches[base.Name]; len(p) > 0 {
			if merged, err := ApplyAppDefPatch(base, p); err == nil {
				apps[i] = merged
			} else {
				slog.Warn("Failed to apply app edit patch; using generated def", "app", base.Name, "err", err)
			}
		}
	}

	// Memory budget + OOM heap-dump flags last, on the EFFECTIVE defs: both are
	// derived from resources.limits.memory and from JAVA_OPTS, and a `citeck edit`
	// patch can change either. Computing them during generation would budget the
	// JVM for the limit the app used to have — raising memoryLimit to 8g would
	// leave the heap capped for 2g. The baseline gets the same treatment so the
	// editor's change gutter still diffs like for like.
	for i := range baselineApps {
		applyJVMRuntimeDefaults(&baselineApps[i])
	}
	for i := range apps {
		applyJVMRuntimeDefaults(&apps[i])
	}

	// Derive def-dependent files from the EFFECTIVE apps, BEFORE the BaselineFiles
	// snapshot and file-edit merge, so the derived conf is the template a user's
	// file-edit delta lands on top of.
	deriveRabbitMemoryConf(ctx, apps)

	// Snapshot the generated (pre-merge) file set as the baseline — the editor
	// serves it as the change-gutter baseline and WriteEditedFile diffs against
	// it. Then merge each user file edit onto its template so BOTH the on-disk
	// file (ctx.Files) and the deployment hash reflect the merged result.
	baselineFiles := make(map[string][]byte, len(ctx.Files))
	maps.Copy(baselineFiles, ctx.Files)
	if len(ctx.EditedFileEdits) > 0 {
		applyFileEditsToFiles(ctx.Files, ctx.EditedFileEdits, ctx.DiskContent)
	}

	// Fill VolumesContentHash for each app so the deployment hash changes
	// when any bind-mount source file's content changes — triggering a
	// container recreate. Mirrors Kotlin's NsRuntimeFiles.getPathsContentHash
	// hooked into ApplicationDef.hashField.
	for i := range apps {
		apps[i].VolumesContentHash = computeVolumesContentHash(&apps[i], ctx.Files)
	}

	// Compute DependsOnDetachedApps: detached apps that are referenced as dependencies
	// by other (non-detached) apps. Restarting these triggers regeneration.
	dependsOnDetached := make(map[string]bool)
	if len(ctx.DetachedApps) > 0 {
		for _, a := range apps {
			if ctx.DetachedApps[a.Name] {
				continue
			}
			for _, dep := range a.DependsOn {
				if ctx.DetachedApps[dep] {
					dependsOnDetached[dep] = true
				}
			}
		}
	}

	var customLinks []bundle.WorkspaceLink
	if ctx.WorkspaceConfig != nil {
		customLinks = ctx.WorkspaceConfig.Links
	}

	return &GenResp{
		Applications:          apps,
		BaselineApplications:  baselineApps,
		Files:                 ctx.Files,
		BaselineFiles:         baselineFiles,
		CloudConfig:           ctx.CloudConfig,
		DependsOnDetachedApps: dependsOnDetached,
		CustomLinks:           customLinks,
		DependencyUpgrades:    sortedUpgrades(ctx),
		Dependencies:          ctx.DependencyImages,
	}, nil
}

// lateBuiltInApps are the built-in apps whose generators run AFTER the bundle
// webapp loop: stt-sidecar (it needs the ai app to exist), the proxy (it reads
// the gateway's resolved port) and onlyoffice (which the proxy depends on).
// Nothing else may name them.
//
// The loop's "does it already have a builder?" test cannot see these three —
// at that point they do not exist yet — so a bundle entry with one of their
// names would be generated as a JVM webapp first and then only PARTIALLY
// overwritten by its own generator, which sets the fields it knows about and
// leaves the rest: nginx carrying SERVER_PORT and a spring-props mount, and,
// because IsJVM survives, a JVM memory budget writing -Xmx into an image with
// no JVM in it. A hybrid container is worse than either definition alone.
var lateBuiltInApps = map[string]bool{
	appdef.AppSttSidecar: true,
	appdef.AppProxy:      true,
	appdef.AppOnlyoffice: true,
}

// isBuiltInApp reports whether name belongs to a built-in generator, in both
// directions: it ALREADY has a builder (every infra app, keycloak, alfresco,
// observer — generated before the webapp loop), or its generator has not run
// yet (lateBuiltInApps). The first arm is not merely cosmetic: it lands AFTER
// resolveDependencyImage has decided which image a dependency's data may run
// on, so a bundle entry named "postgres" would put the held-back candidate into
// the container while GenResp still reports the pin as effective.
//
// Both arms bite in the same situation — a workspace config that lists no
// webapps at all, which makes Generate's filter admit every bundle application,
// infra included.
func isBuiltInApp(ctx *NsGenContext, name string) bool {
	if _, exists := ctx.Applications[name]; exists {
		return true
	}
	return lateBuiltInApps[name]
}

// pruneAppsWithMissingDeps removes any app whose dependsOn references an app that
// is not present in the generated set, transitively (runs to a fixpoint).
//
// Every built-in generator either points dependsOn at an always-generated infra/core
// app, guards the dep on the target's existence (proxy's ai/alfresco guards), or
// co-generates the target (alf-postgres with alfresco, stt-sidecar before the ai
// dep). The one hard cross-app dep is proxy→gateway: the gateway is always present
// in real bundles, and where it isn't, the proxy serves no purpose, so pruning it is
// correct. So in practice this pass removes only additionalApps that reference a
// non-existent dependency (a typo, a mode-disabled app, or another pruned app).
//
// Transitive: in a chain A→B→C where C is missing, B is removed (depends on absent
// C), which makes A's dep B absent, so A is removed too. The outer loop re-runs
// until a full pass removes nothing, so removals propagate regardless of map order.
//
// Must run after every generator has populated ctx.Applications.
func pruneAppsWithMissingDeps(ctx *NsGenContext) {
	for {
		removed := false
		for name, app := range ctx.Applications {
			for _, dep := range app.DependsOn {
				if _, present := ctx.Applications[dep]; !present {
					slog.Error("app dependsOn an app not present in the generated set; excluding it from the namespace",
						"name", name, "missingDep", dep)
					delete(ctx.Applications, name)
					removed = true
					break
				}
			}
		}
		if !removed {
			return
		}
	}
}

// sortedKeys returns a plain map's keys in sorted order — used at env-build
// sites that source from an unordered map so the generated baseline order is
// deterministic (operator-arranged order is preserved separately via the
// per-app config edit, which uses appdef.OrderedMap).
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// applyFileEditsToFiles merges each persisted file edit onto its generated
// template in `files`, in place. `disk` supplies the on-disk content used as the
// YAML comment source and textual conflict fallback. Keys absent from `files`
// (the template no longer emits that path) are skipped.
func applyFileEditsToFiles(files map[string][]byte, edits map[string]FileEdit, disk map[string][]byte) {
	for key, edit := range edits {
		template, ok := files[key]
		if !ok {
			continue
		}
		base := key[strings.LastIndex(key, "/")+1:]
		merged, err := ApplyFileEdit(base, edit, template, disk[key])
		if err != nil {
			slog.Warn("Failed to apply file edit; keeping template", "key", key, "err", err)
			continue
		}
		files[key] = merged
	}
}
