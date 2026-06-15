package namespace

import (
	"fmt"
	goruntime "runtime"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// ToNamespaceDto converts the runtime state to an API DTO.
func (r *Runtime) ToNamespaceDto() api.NamespaceDto {
	r.mu.RLock()
	defer r.mu.RUnlock()
	apps := make([]api.AppDto, 0, len(r.apps))
	for _, app := range r.apps {
		_, edited := r.editedApps[app.Name]
		// Memory thresholds match the Kotlin per-app StatsCell tooltip
		// boundaries (80% warning / 95% critical) — see ContainerStatViews.kt.
		memPct := app.MemoryPercent
		initStep, initTotal, initName := appInitProgress(app)
		apps = append(apps, api.AppDto{
			Name:             app.Name,
			Status:           displayAppStatus(app),
			StatusText:       app.StatusText,
			Image:            app.Def.Image,
			CPU:              app.CPU,
			Memory:           app.Memory,
			MemoryPercent:    memPct,
			MemoryWarning:    memPct >= 80 && memPct < 95,
			MemoryCritical:   memPct >= 95,
			CPUThrottled:     app.CPUThrottled,
			Kind:             KindToString(app.Def.Kind),
			Ports:            app.Def.Ports,
			Edited:           edited,
			Locked:           r.editedLockedApps[app.Name],
			RestartCount:     app.RestartCount,
			EditedFilesCount: len(r.editedFilesForAppLocked(app.Name)),
			InitStep:         initStep,
			InitTotal:        initTotal,
			InitName:         initName,
		})
	}
	return api.NamespaceDto{
		ID:        r.nsID,
		Name:      r.config.Name,
		Status:    string(r.status),
		BundleRef: ResolveDisplayBundleRef(r.config.BundleRef, r.cachedBundle),
		Apps:      apps,
		Links:     r.generateLinks(),
		// Host CPU count for the UI's aggregate CPU progress-bar cap. Pulled
		// from the Go runtime — matches Docker stats' OnlineCPUs as long as
		// no cpuset restrictions are set on the daemon, which is the case
		// in every supported deployment.
		HostCPUs: goruntime.NumCPU(),
	}
}

// ResolveDisplayBundleRef returns the bundle ref string to show in the UI,
// substituting the resolved concrete version for a symbolic "LATEST" key when a
// cached (already-resolved) bundle is available. In Kotlin 1.x the stored ref
// was concrete (LATEST resolved at create — WelcomeScreen.kt:293), so the
// dashboard naturally showed the real version. Here a namespace config can
// still carry "LATEST" (existing namespaces, workspace templates, manual YAML
// edits), so we resolve it for display from the runtime's cached bundle. When
// no cached version is available, the raw ref is returned unchanged.
func ResolveDisplayBundleRef(ref bundle.Ref, cached *bundle.Def) string {
	if strings.EqualFold(ref.Key, "LATEST") && cached != nil && cached.Key.Version != "" {
		return ref.Repo + ":" + cached.Key.Version
	}
	return ref.String()
}

// displayAppStatus rewrites an app's runtime status into the user-facing
// status the UI should render. Recreate-in-flight is its own first-class
// state now (UPDATING) — set directly by doRegenerate, T17a liveness
// restart, RestartApp, and the STOPPING_FAILED retry — so we no longer mask
// STOPPING with desiredNext as the UI cue. UPDATING is surfaced as-is so
// the dashboard and the daemon log read the same way the state machine
// actually behaves.
func displayAppStatus(app *AppRuntime) string {
	return string(app.Status)
}

// appInitProgress maps the runtime's ephemeral init-phase state onto the
// AppDto init fields: 1-based current step, total init containers, and a short
// step name. Returns zeros unless the app is STARTING with the init phase
// active (initActive is set in beginStartingUnderLock and cleared at T12 /
// on leaving STARTING — see setAppStatus). Caller must hold r.mu (read or
// write); the fields read here are only mutated on runtimeLoop under r.mu.
func appInitProgress(app *AppRuntime) (step, total int, name string) {
	if !app.initActive || app.Status != AppStatusStarting {
		return 0, 0, ""
	}
	total = len(app.Def.InitContainers)
	idx := app.initStepIdx
	if total == 0 || idx < 0 || idx >= total {
		// Defensive: stale flag or out-of-range index — report "no init phase"
		// rather than panicking on InitContainers[idx].
		return 0, 0, ""
	}
	return idx + 1, total, initStepDisplayName(app.Def.InitContainers[idx].Image)
}

// initStepDisplayName derives a short human-readable init step name from the
// init container image reference. InitContainerDef has no name field, so the
// image's last path segment without tag/digest is the best stable label:
// "registry.citeck.ru/citeck/ecos-app-x:1.2.3" → "ecos-app-x".
func initStepDisplayName(image string) string {
	name := image
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "@"); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, ":"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return image
	}
	return name
}

// AppliedConfig returns the config currently driving this runtime (the "applied" config).
// Returns nil if the runtime was never started.
func (r *Runtime) AppliedConfig() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config
}

// AppImages returns the distinct container images of the namespace's apps.
// Used by the registry pre-start check so it prompts only for registries the
// namespace's images actually pull from (not every auth-declared workspace repo).
func (r *Runtime) AppImages() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{}, len(r.apps))
	out := make([]string, 0, len(r.apps))
	for _, app := range r.apps {
		img := app.Def.Image
		if img == "" {
			continue
		}
		if _, ok := seen[img]; ok {
			continue
		}
		seen[img] = struct{}{}
		out = append(out, img)
	}
	return out
}

// KindToString converts an ApplicationKind to its API string representation.
func KindToString(k appdef.ApplicationKind) string {
	switch k {
	case appdef.KindCiteckCore:
		return "CITECK_CORE"
	case appdef.KindCiteckCoreExtension:
		return "CITECK_CORE_EXTENSION"
	case appdef.KindCiteckAdditional:
		return "CITECK_ADDITIONAL"
	case appdef.KindThirdParty:
		return "THIRD_PARTY"
	default:
		return "UNKNOWN"
	}
}

// generateLinks builds quick links. Must be called with r.mu held.
func (r *Runtime) generateLinks() []api.LinkDto {
	if r.config == nil {
		return nil
	}
	proxyBase := r.proxyBaseURL()
	proxyHost := r.config.Proxy.Host
	if proxyHost == "" {
		proxyHost = "localhost"
	}

	// Categories mirror Kotlin `NamespaceLink.category` grouping.
	// Empty category = top of the list, no header.
	const catApps = "Apps"
	const catResources = "Resources"

	// Icon names are looked up by `/icons/<name>.svg` in the Web UI; the SVGs
	// are bundled from the Kotlin launcher's resources/icons/app/ folder.
	// DescriptionKey carries an i18n key the Web UI resolves to a localized
	// hover tooltip (Kotlin v1 parity: links showed a description + default
	// credentials). Description keeps an English fallback for non-UI consumers.
	links := []api.LinkDto{
		{Name: "Citeck UI", URL: proxyBase, Order: -100},
		{Name: "Spring Boot Admin", URL: proxyBase + "/gateway/eapps/admin/wallboard", Icon: "spring-boot-admin", Order: -1, Category: catApps, DescriptionKey: "links.springBootAdmin.tooltip"},
		{Name: "RabbitMQ", URL: fmt.Sprintf("http://%s:15672", proxyHost), Icon: "rabbitmq", Order: 2, Category: catApps, DescriptionKey: "links.rabbitmq.tooltip"},
		{Name: "Mailpit", URL: fmt.Sprintf("http://%s:8025", proxyHost), Icon: "mailpit", Order: 1, Category: catApps, DescriptionKey: "links.mailpit.tooltip"},
	}

	// Keycloak link (only if auth is KEYCLOAK)
	if r.config.Authentication.Type == AuthKeycloak {
		links = append(links, api.LinkDto{
			Name: "Keycloak Admin", URL: proxyBase + "/ecos-idp/auth/", Icon: "keycloak", Order: -10, Category: catApps, DescriptionKey: "links.keycloak.tooltip",
		})
	}

	// PgAdmin link (if app exists)
	if _, ok := r.apps["pgadmin"]; ok {
		links = append(links, api.LinkDto{
			Name: "PG Admin", URL: fmt.Sprintf("http://%s:5050", proxyHost), Icon: "postgres", Order: 0, Category: catApps, DescriptionKey: "links.pgAdmin.tooltip",
		})
	}

	// Global links (always available) — Kotlin parity: GlobalLinks.LINKS
	links = append(links,
		api.LinkDto{Name: "Documentation", URL: "https://citeck-ecos.readthedocs.io/", Icon: "docs", Order: 100, Category: catResources, Description: "Citeck documentation", DescriptionKey: "links.documentation.tooltip", AlwaysEnabled: true},
		api.LinkDto{Name: "AI Documentation Bot", URL: "https://t.me/haski_citeck_bot", Icon: "telegram", Order: 101, Category: catResources, Description: "Telegram bot for AI documentation assistance", DescriptionKey: "links.aiBot.tooltip", AlwaysEnabled: true},
	)

	return links
}

func (r *Runtime) proxyBaseURL() string {
	return BuildProxyBaseURL(r.config.Proxy)
}
