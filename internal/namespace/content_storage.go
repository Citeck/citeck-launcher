package namespace

import (
	"log/slog"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// Content storage: where ecos-content keeps a namespace's files. DB (the
// default) needs nothing from the launcher. S3 runs the workspace's S3
// provider — an additionalApps entry the workspace names in its
// `contentStorage.s3` block — beside the stand, makes content wait for it, and
// (in the daemon) connects ecos-content to it once.
//
// The launcher knows the ECOS side of that contract (the `content` app and the
// records it is configured through) and NOTHING about the provider: which
// container serves S3 is configuration, the rule the observer established. No
// provider name is spelled here.

// Why S3 content storage is not available, as a wire code. The UI hides the
// option for NO_CONTENT_APP (the bundle has no ecos-content, so the question
// does not exist — Community) and shows it disabled, with a hint, for the rest.
const (
	ContentStorageNoContentApp     = "NO_CONTENT_APP"
	ContentStorageNoProviderConfig = "NO_PROVIDER_CONFIG"
	ContentStorageNoProviderApp    = "NO_PROVIDER_APP"
	ContentStorageNoProviderImage  = "NO_PROVIDER_IMAGE"
	// ContentStorageProviderConflict: the provider cannot be part of the
	// stand as the workspace declares it — its name is a platform app's (a
	// built-in, or a webapp the workspace lists and the bundle carries, which
	// the generator keeps and skips the entry for), or it depends on
	// ecos-content itself, which content's own dependency on it would turn
	// into a cycle.
	ContentStorageProviderConflict = "PROVIDER_CONFLICT"
	// ContentStorageProviderNotGenerated is a GENERATION problem, never an
	// availability answer: the configuration offers a provider, but this
	// generation cannot wire it — a provider or connection `${secret:}` has
	// no namespace value, or something the provider depends on is missing.
	ContentStorageProviderNotGenerated = "PROVIDER_NOT_GENERATED"
)

// ContentStorageAvailability answers whether a namespace on a given bundle and
// workspace may store its content in S3.
type ContentStorageAvailability struct {
	Available bool
	// Reason is one of the ContentStorageNo* codes, "" when Available.
	Reason string
	// ProviderApp is the workspace's `contentStorage.s3.app`, "" when the
	// workspace names none.
	ProviderApp string
	// ProviderTitle is the provider's name for a label (`contentStorage.s3.
	// title`, else the app id), "" when the workspace names none.
	ProviderTitle string
}

// OptionLabel is the S3 option's label, "S3 (<provider title>)", or plain
// "S3" while the workspace names no provider. Both the CLI and the web build
// it the same way, from configuration: the launcher spells no product name.
func (a ContentStorageAvailability) OptionLabel() string {
	return S3OptionLabel(a.ProviderTitle, a.ProviderApp)
}

// S3OptionLabel is OptionLabel for a title and an app id read from a DTO: the
// title, else the app id, else nothing.
func S3OptionLabel(title, app string) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = strings.TrimSpace(app)
	}
	if name == "" {
		return "S3"
	}
	return "S3 (" + name + ")"
}

// ContentStorageS3Availability is the one answer to "may this namespace store
// its content in S3", shared by the create/edit write gate, the options the
// dialogs offer and the generator's own problem report. It reads the bundle and
// the workspace and nothing else — no Docker, no git.
//
// The checks run in the order the UI needs them: a bundle without ecos-content
// HIDES the option, so that answer must win over every configuration problem.
// The provider's image is resolved by the precedence generateAdditionalApps
// applies (additionalAppImage), and a name the generator's collision guard
// would skip the entry for is refused, so "available" here means the
// generator will really emit the container.
func ContentStorageS3Availability(bun *bundle.Def, ws *bundle.WorkspaceConfig) ContentStorageAvailability {
	var out ContentStorageAvailability
	if s3 := ws.ContentStorageS3(); s3 != nil {
		out.ProviderApp = s3.App
		out.ProviderTitle = s3.DisplayName()
	}
	switch {
	case bun == nil || bun.Applications[appdef.AppContent].Image == "":
		out.Reason = ContentStorageNoContentApp
	case out.ProviderApp == "":
		out.Reason = ContentStorageNoProviderConfig
	default:
		entry, ok := ws.AdditionalApp(out.ProviderApp)
		switch {
		case !ok:
			out.Reason = ContentStorageNoProviderApp
		case providerNameTaken(bun, ws, out.ProviderApp) || dependsOnContent(ws, entry):
			out.Reason = ContentStorageProviderConflict
		case additionalAppImage(bun, ws, entry) == "":
			out.Reason = ContentStorageNoProviderImage
		default:
			out.Available = true
		}
	}
	return out
}

// providerNameTaken reports whether the generator would keep a platform app
// under this name and skip the provider's entry (generateAdditionalApps'
// collision guard): a built-in, or a bundle webapp the workspace lists in
// `webapps:` — a listed webapp is never claimed by an additionalApps entry.
// The guard itself sees the full generated set; this is the part of it the
// bundle and the workspace already answer, which is what the write gate has.
func providerNameTaken(bun *bundle.Def, ws *bundle.WorkspaceConfig, name string) bool {
	if name == appdef.AppContent || bundle.IsReservedAppName(name) {
		return true
	}
	if _, inBundle := bun.Applications[name]; !inBundle || ws == nil {
		return false
	}
	return slices.ContainsFunc(ws.Webapps, func(w bundle.WebappConfig) bool { return w.ID == name })
}

// dependsOnContent reports whether a workspace entry depends on ecos-content,
// directly or through other raw additionalApps entries. Content's dependency
// on its provider would close that into a cycle. Apps outside the workspace's
// entries are not followed here — the generation walk (wireContentStorage)
// sees the whole generated set.
func dependsOnContent(ws *bundle.WorkspaceConfig, entry bundle.AdditionalAppProps) bool {
	visited := map[string]bool{strings.TrimSpace(entry.Name): true}
	queue := slices.Clone(entry.DependsOn)
	for len(queue) > 0 {
		dep := strings.TrimSpace(queue[0])
		queue = queue[1:]
		if dep == appdef.AppContent {
			return true
		}
		if visited[dep] {
			continue
		}
		visited[dep] = true
		if next, ok := ws.AdditionalApp(dep); ok {
			queue = append(queue, next.DependsOn...)
		}
	}
	return false
}

// RequiredApps lists the apps this namespace's own configuration requires to
// run, which a workspace template's detachedApps must therefore not hold back:
// today the S3 provider of an S3 namespace. It answers from the configuration
// alone — whether the bundle really produces the app is the generator's
// question, and a name that is not generated subtracts nothing.
func RequiredApps(cfg *Config, ws *bundle.WorkspaceConfig) []string {
	if cfg.ContentStorageType() != ContentStorageS3 {
		return nil
	}
	if s3 := ws.ContentStorageS3(); s3 != nil && s3.App != "" {
		return []string{s3.App}
	}
	return nil
}

// ContentStorageProblem is why an S3 namespace's generation could not wire
// ecos-content to its provider. It is REPORTED (NamespaceDto.ContentStorage),
// never a Generate error: an error there fails loadNamespace and can stop the
// daemon at boot over a provider the operator may simply have to unlock a
// vault for.
type ContentStorageProblem struct {
	// Reason is a ContentStorageNo* code or ContentStorageProviderNotGenerated.
	Reason string
	// ProviderApp is the provider the workspace names, "" when it names none.
	ProviderApp string
}

// wireContentStorage makes ecos-content of an S3 namespace wait for the S3
// provider and supplies native endpoint/credential env. The daemon selects
// the built-in S3 default once the platform is up. No gating app is added.
//
// It must run AFTER excludeAppsMissingSecrets and BEFORE
// pruneAppsWithMissingDeps. The provider references the namespace's S3 secret,
// so it is exactly the app the secrets pass removes when the vault cannot
// answer; a dependsOn pointing at it then would make the prune delete content,
// and with it everything that depends on content. For the same reason the
// provider must also SURVIVE the prune: a provider whose own dependency is
// missing is about to be removed, and wiring content to it would take content
// along. In both cases content stays and the problem is reported instead.
//
// A DB namespace is untouched: its content def, and therefore its deployment
// hash, is byte-identical to what every launcher before this one generated.
func wireContentStorage(ctx *NsGenContext) {
	if ctx.Config.ContentStorageType() != ContentStorageS3 {
		return
	}
	avail := ContentStorageS3Availability(ctx.Bundle, ctx.WorkspaceConfig)
	content, ok := ctx.Applications[appdef.AppContent]
	if !ok {
		// No ecos-content in this generation: there is nothing to wire, and the
		// option itself is unavailable. Said, so the namespace does not wait
		// for a connection that cannot happen.
		ctx.ContentStorageProblem = &ContentStorageProblem{
			Reason: ContentStorageNoContentApp, ProviderApp: avail.ProviderApp,
		}
		return
	}
	if !avail.Available {
		ctx.ContentStorageProblem = &ContentStorageProblem{Reason: avail.Reason, ProviderApp: avail.ProviderApp}
		slog.Warn("S3 content storage is not available; ecos-content is not wired to a provider",
			"reason", avail.Reason, "provider", avail.ProviderApp)
		return
	}
	if ctx.collidedAdditionalApps[avail.ProviderApp] {
		// The generator kept a platform app under the provider's name and
		// skipped the entry: the app that exists is not the S3 server.
		ctx.ContentStorageProblem = &ContentStorageProblem{
			Reason: ContentStorageProviderConflict, ProviderApp: avail.ProviderApp,
		}
		slog.Warn("The S3 content storage provider's name belongs to a platform app; ecos-content is not wired to it",
			"provider", avail.ProviderApp)
		return
	}
	survives, reachesContent := providerClosure(ctx, avail.ProviderApp)
	switch {
	case reachesContent:
		// content → provider → … → content: wiring it would be a dependsOn
		// cycle, which fails the whole generation (detectDependencyCycles) —
		// over a workspace entry the operator may not even be able to edit.
		ctx.ContentStorageProblem = &ContentStorageProblem{
			Reason: ContentStorageProviderConflict, ProviderApp: avail.ProviderApp,
		}
		slog.Warn("The S3 content storage provider depends on ecos-content; ecos-content is not wired to it",
			"provider", avail.ProviderApp)
	case !survives:
		ctx.ContentStorageProblem = &ContentStorageProblem{
			Reason: ContentStorageProviderNotGenerated, ProviderApp: avail.ProviderApp,
		}
		slog.Warn("The S3 content storage provider is not part of this generation; ecos-content is not wired to it",
			"provider", avail.ProviderApp)
	default:
		s3 := ctx.WorkspaceConfig.ContentStorageS3()
		access, missingAccess := ResolveSecretRefs(s3.AccessKey, ctx.NamespaceSecrets)
		secret, missingSecret := ResolveSecretRefs(s3.SecretKey, ctx.NamespaceSecrets)
		if len(missingAccess)+len(missingSecret) > 0 {
			ctx.ContentStorageProblem = &ContentStorageProblem{Reason: ContentStorageProviderNotGenerated, ProviderApp: avail.ProviderApp}
			return
		}
		content.AddEnv("ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_URL", s3.URL)
		content.AddEnv("ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_CREDENTIALS", "content-storage-s3-credentials")
		content.AddEnv("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_TYPE", "BASIC")
		content.AddEnv("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_USERNAME", access)
		content.AddEnv("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD", secret)
		content.AddDependsOn(avail.ProviderApp)
	}
}

// providerClosure walks the provider's dependsOn closure in the generated
// set. survives is whether the app is generated and will be kept by
// pruneAppsWithMissingDeps (every app it depends on, transitively, is
// generated too); reachesContent is whether ecos-content is in the closure,
// i.e. whether content's dependency on the provider would close a cycle. The
// visited set keeps an existing cycle (refused later by detectDependencyCycles)
// from spinning here first.
func providerClosure(ctx *NsGenContext, provider string) (survives, reachesContent bool) {
	if provider == appdef.AppContent {
		return true, true
	}
	visited := make(map[string]bool)
	survives = true
	var walk func(string)
	walk = func(app string) {
		if visited[app] {
			return
		}
		visited[app] = true
		if app == appdef.AppContent {
			reachesContent = true
			return
		}
		b, ok := ctx.Applications[app]
		if !ok {
			survives = false
			return
		}
		for _, dep := range b.DependsOn {
			walk(dep)
		}
	}
	walk(provider)
	return survives, reachesContent
}
