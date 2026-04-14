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
		apps = append(apps, api.AppDto{
			Name:         app.Name,
			Status:       string(app.Status),
			StatusText:   app.StatusText,
			Image:        app.Def.Image,
			CPU:          app.CPU,
			Memory:       app.Memory,
			Kind:         KindToString(app.Def.Kind),
			Ports:        app.Def.Ports,
			Edited:       edited,
			Locked:       r.editedLockedApps[app.Name],
			RestartCount: app.RestartCount,
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

	links := []api.LinkDto{
		{Name: "Citeck UI", URL: proxyBase, Icon: "ecos", Order: -100},
		{Name: "Spring Boot Admin", URL: proxyBase + "/gateway/eapps/admin/wallboard", Icon: "spring", Order: -1},
		{Name: "RabbitMQ", URL: fmt.Sprintf("http://%s:15672", proxyHost), Icon: "rabbitmq", Order: 2},
		{Name: "MailHog", URL: fmt.Sprintf("http://%s:8025", proxyHost), Icon: "mailhog", Order: 1},
	}

	// Keycloak link (only if auth is KEYCLOAK)
	if r.config.Authentication.Type == AuthKeycloak {
		links = append(links, api.LinkDto{
			Name: "Keycloak Admin", URL: proxyBase + "/ecos-idp/auth/", Icon: "keycloak", Order: -10,
		})
	}

	// PgAdmin link (if app exists)
	if _, ok := r.apps["pgadmin"]; ok {
		links = append(links, api.LinkDto{
			Name: "PG Admin", URL: fmt.Sprintf("http://%s:5050", proxyHost), Icon: "postgres", Order: 0,
		})
	}

	// Global links (always available)
	links = append(links,
		api.LinkDto{Name: "Documentation", URL: "https://citeck-ecos.readthedocs.io/", Icon: "docs", Order: 100},
		api.LinkDto{Name: "AI Documentation Bot", URL: "https://t.me/haski_citeck_bot", Icon: "telegram", Order: 101},
	)

	return links
}

func (r *Runtime) proxyBaseURL() string {
	return BuildProxyBaseURL(r.config.Proxy)
}
