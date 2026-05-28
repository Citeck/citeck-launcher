package namespace

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
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
		apps = append(apps, api.AppDto{
			Name:             app.Name,
			Status:           displayAppStatus(app, r.manualStoppedApps[app.Name]),
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
		})
	}
	return api.NamespaceDto{
		ID:        r.nsID,
		Name:      r.config.Name,
		Status:    string(r.status),
		BundleRef: r.config.BundleRef.String(),
		Apps:      apps,
		Links:     r.generateLinks(),
	}
}

// displayAppStatus rewrites an app's runtime status into the user-facing
// status the UI should render. The state machine occasionally walks an app
// through STOPPING / STOPPED as part of a runtime-initiated recreate
// (hash-changed regenerate, T17a liveness-restart, RestartApp). From the
// user's perspective the app is "starting", not "stopping" — they didn't
// ask for a stop. So:
//
//   - STOPPING with desiredNext set (runtime-initiated recreate) → render as
//     PULLING. The container is being torn down only to be re-pulled and
//     re-started in the same logical operation.
//   - STOPPING when the user explicitly detached (manualStoppedApps) → render
//     as-is so the user sees their action take effect.
//
// initialSweep is an orthogonal hint (set on the runtime-initiated recreate
// branch in doRegenerate / RestartApp); we treat it as equivalent to a
// non-empty desiredNext for display purposes.
func displayAppStatus(app *AppRuntime, manualStopped bool) string {
	if manualStopped {
		return string(app.Status)
	}
	if app.Status == AppStatusStopping && (app.initialSweep || app.desiredNext != "") {
		// Recreate in flight — show "pulling" since the next leg is
		// pull → start, regardless of the specific desiredNext value.
		return string(AppStatusPulling)
	}
	return string(app.Status)
}

// AppliedConfig returns the config currently driving this runtime (the "applied" config).
// Returns nil if the runtime was never started.
func (r *Runtime) AppliedConfig() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config
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
	links := []api.LinkDto{
		{Name: "Citeck UI", URL: proxyBase, Order: -100},
		{Name: "Spring Boot Admin", URL: proxyBase + "/gateway/eapps/admin/wallboard", Icon: "spring-boot-admin", Order: -1, Category: catApps},
		{Name: "RabbitMQ", URL: fmt.Sprintf("http://%s:15672", proxyHost), Icon: "rabbitmq", Order: 2, Category: catApps},
		{Name: "MailHog", URL: fmt.Sprintf("http://%s:8025", proxyHost), Icon: "mailhog", Order: 1, Category: catApps},
	}

	// Keycloak link (only if auth is KEYCLOAK)
	if r.config.Authentication.Type == AuthKeycloak {
		links = append(links, api.LinkDto{
			Name: "Keycloak Admin", URL: proxyBase + "/ecos-idp/auth/", Icon: "keycloak", Order: -10, Category: catApps,
		})
	}

	// PgAdmin link (if app exists)
	if _, ok := r.apps["pgadmin"]; ok {
		links = append(links, api.LinkDto{
			Name: "PG Admin", URL: fmt.Sprintf("http://%s:5050", proxyHost), Icon: "postgres", Order: 0, Category: catApps,
		})
	}

	// Global links (always available) — Kotlin parity: GlobalLinks.LINKS
	links = append(links,
		api.LinkDto{Name: "Documentation", URL: "https://citeck-ecos.readthedocs.io/", Icon: "docs", Order: 100, Category: catResources, Description: "Citeck documentation", AlwaysEnabled: true},
		api.LinkDto{Name: "AI Documentation Bot", URL: "https://t.me/haski_citeck_bot", Icon: "telegram", Order: 101, Category: catResources, Description: "Telegram bot for AI documentation assistance", AlwaysEnabled: true},
	)

	return links
}

func (r *Runtime) proxyBaseURL() string {
	return BuildProxyBaseURL(r.config.Proxy)
}
