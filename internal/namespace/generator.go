package namespace

// Generator orchestration. Per-service-family generators live in sibling
// files: generator_infra.go (mailpit / mongo / pgadmin / postgres /
// zookeeper / rabbitmq), generator_keycloak.go, generator_proxy.go,
// generator_webapp.go (bundle webapps + alfresco / observer / stt-sidecar /
// onlyoffice), generator_util.go (template-var resolution, license merging,
// YAML helpers, content hashing).

import (
	"fmt"
	"maps"
	"sort"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
)

// GenResp is the result of namespace generation.
type GenResp struct {
	Applications          []appdef.ApplicationDef
	Files                 map[string][]byte
	CloudConfig           map[string]map[string]any // per-app ext cloud config for CloudConfigServer
	DependsOnDetachedApps map[string]bool           // apps whose reattachment triggers regeneration
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
	// EditedFileOverlay maps canonical ctx.Files keys ("<app>/<rel>", no leading
	// "./") to the on-disk content the user authored via the Web UI. The
	// generator overlays these onto ctx.Files BEFORE computing per-app
	// VolumesContentHash so a UI-edit forces a container recreate. Without this
	// overlay, the hash is computed against the embedded defaults and a user
	// edit silently does nothing on the next reload — matching Kotlin's
	// NsRuntimeFiles which re-hashes from the on-disk volumeFiles snapshot.
	EditedFileOverlay map[string][]byte
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
		ctx.EditedFileOverlay = opts[0].EditedFileOverlay
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
		generateWebapp(name, ctx)
	}

	// STT sidecar (speech-to-text proxy for AI websocket traffic) — must run
	// AFTER the AI webapp is generated because it injects an env var + dep
	// onto the AI app, and before generateProxy so a future AI_TARGET wiring
	// in the proxy can read the resolved STT port from the apps map.
	generateSttSidecar(ctx)

	// Generate proxy (depends on gateway, onlyoffice)
	generateProxy(ctx)
	generateOnlyOffice(ctx)

	// Server mode: only proxy publishes ports — all other apps are internal to Docker network.
	// Desktop mode: all ports published for local debugging (CloudConfigServer, direct DB access, etc.)
	if !config.IsDesktopMode() {
		for _, b := range ctx.Applications {
			if b.Name != appdef.AppProxy {
				b.Ports = nil
			}
		}
	}

	// Build all applications
	apps := make([]appdef.ApplicationDef, 0, len(ctx.Applications))
	for _, b := range ctx.Applications {
		apps = append(apps, b.Build())
	}

	// Overlay user-edited file content into a hash-only view of ctx.Files so
	// the deployment hash reflects on-disk content. ctx.Files itself is left
	// untouched — writeRuntimeFiles already skips edited keys via its own
	// snapshot, and re-running the generator must remain deterministic w.r.t.
	// the embedded defaults regardless of disk state.
	hashFiles := ctx.Files
	if len(ctx.EditedFileOverlay) > 0 {
		hashFiles = make(map[string][]byte, len(ctx.Files)+len(ctx.EditedFileOverlay))
		maps.Copy(hashFiles, ctx.Files)
		maps.Copy(hashFiles, ctx.EditedFileOverlay)
	}

	// Fill VolumesContentHash for each app so the deployment hash changes
	// when any bind-mount source file's content changes — triggering a
	// container recreate. Mirrors Kotlin's NsRuntimeFiles.getPathsContentHash
	// hooked into ApplicationDef.hashField.
	for i := range apps {
		apps[i].VolumesContentHash = computeVolumesContentHash(&apps[i], hashFiles)
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

	return &GenResp{
		Applications:          apps,
		Files:                 ctx.Files,
		CloudConfig:           ctx.CloudConfig,
		DependsOnDetachedApps: dependsOnDetached,
	}, nil
}
