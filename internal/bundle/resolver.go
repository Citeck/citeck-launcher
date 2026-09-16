package bundle

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/git"
	"gopkg.in/yaml.v3"
)

// ErrNoBundles signals that a bundle repo's pull succeeded but no bundle
// definitions were found on disk (either the bundles directory does not
// exist, or it exists but has no .yml/.yaml files matching a version
// pattern). Callers that only use Resolve(LATEST) as a "did the pull work"
// probe can treat this as a benign outcome via errors.Is(err, ErrNoBundles).
var ErrNoBundles = errors.New("no bundles found")

// DefaultBundlesRepo is the canonical launcher-workspace git remote. Exported
// so daemon code (workspace CRUD routes, helpers) can reuse the same value
// without duplicating the URL.
const DefaultBundlesRepo = "https://github.com/Citeck/launcher-workspace.git"

// DefaultBundlesBranch is the canonical branch on DefaultBundlesRepo.
const DefaultBundlesBranch = "main"

const (
	defaultPullPeriod = time.Hour
)

// ImageRepo maps a short prefix like "core" to a full registry URL like "nexus.citeck.ru".
type ImageRepo struct {
	ID       string `yaml:"id"`
	URL      string `yaml:"url"`
	AuthType string `yaml:"authType,omitempty"` // "BASIC" if registry requires authentication
}

// DataSourceConfig describes a datasource with at least a URL.
type DataSourceConfig struct {
	URL string `yaml:"url"`
	XA  bool   `yaml:"xa,omitempty"`
}

// WebappDefaultProps holds default properties for a webapp from workspace config.
// Field set mirrors the Kotlin 1.x WebappProps so a workspace.defaultProps block
// authored against the 1.x schema isn't silently dropped (yaml.v3 ignores unknown
// keys). The generator's 3-layer merge applies these under the namespace-level
// overrides.
type WebappDefaultProps struct {
	Image          string                      `yaml:"image"`
	HeapSize       string                      `yaml:"heapSize"`
	MemoryLimit    string                      `yaml:"memoryLimit"`
	Environments   map[string]string           `yaml:"environments"`
	DataSources    map[string]DataSourceConfig `yaml:"dataSources"`
	Enabled        *bool                       `yaml:"enabled,omitempty"`
	ServerPort     int                         `yaml:"serverPort,omitempty"`
	JavaOpts       string                      `yaml:"javaOpts,omitempty"`
	SpringProfiles string                      `yaml:"springProfiles,omitempty"`
	DebugPort      int                         `yaml:"debugPort,omitempty"`
	// CloudConfig holds Spring-style arbitrary per-webapp config keys merged
	// into the eapps/<app>/props/application-launcher.yml. The generator
	// deep-merges three layers in priority order: workspace.defaultWebappProps
	// (global) → workspace.webapps[id].defaultProps (per-app default) →
	// namespace.webapps[id] (per-app override). Object values are merged
	// recursively; scalars are last-write-wins. Mirrors Kotlin's
	// WebappProps.cloudConfig (DataValue) field — without this layer, UI
	// licenses and other per-app workspace overrides would never reach
	// webapps.
	CloudConfig map[string]any `yaml:"cloudConfig,omitempty"`
	// DependsOn lists apps this webapp must wait for, on top of the ones the
	// generator always adds (zookeeper, rabbitmq, postgres, keycloak). Structural,
	// not a value default — but it lives here because this struct is the per-app
	// workspace layer that namespace.yml already overrides.
	DependsOn []string `yaml:"dependsOn,omitempty"`
}

// WebappConfig describes a webapp with its aliases and default props.
type WebappConfig struct {
	ID           string             `yaml:"id"`
	Aliases      []string           `yaml:"aliases"`
	DefaultProps WebappDefaultProps `yaml:"defaultProps"`
}

// ProxyConfig describes the proxy app aliases.
type ProxyConfig struct {
	Aliases []string `yaml:"aliases"`
}

// QuickStartVariant describes a quick start option from workspace config.
type QuickStartVariant struct {
	Name     string `yaml:"name"`
	Snapshot string `yaml:"snapshot,omitempty"`
	Bundle   Ref    `yaml:"bundleRef,omitempty"`
	Template string `yaml:"template,omitempty"`
}

// NamespaceTemplate describes a pre-configured namespace template.
type NamespaceTemplate struct {
	ID           string         `yaml:"id"`
	Name         string         `yaml:"name,omitempty"`
	Config       map[string]any `yaml:"config,omitempty"`
	DetachedApps []string       `yaml:"detachedApps,omitempty"`
}

// BundlesRepo describes a git repository containing bundle definitions.
type BundlesRepo struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	URL        string `yaml:"url,omitempty"`
	Branch     string `yaml:"branch,omitempty"`
	Path       string `yaml:"path,omitempty"`
	AuthType   string `yaml:"authType,omitempty"`
	PullPeriod string `yaml:"pullPeriod,omitempty"` // e.g. "30m", "2h" — defaults to 1h
}

// SnapshotDef describes a downloadable snapshot from workspace config.
type SnapshotDef struct {
	ID     string `yaml:"id" json:"id"`
	Name   string `yaml:"name" json:"name"`
	URL    string `yaml:"url" json:"url"`
	Size   string `yaml:"size,omitempty" json:"size,omitempty"`
	SHA256 string `yaml:"sha256,omitempty" json:"sha256,omitempty"`
}

// PostgresProps holds workspace-level overrides for the PostgreSQL container.
type PostgresProps struct {
	Image ImageRef `yaml:"image,omitempty"`
}

// KeycloakProps holds workspace-level overrides for the Keycloak container.
type KeycloakProps struct {
	Image ImageRef `yaml:"image,omitempty"`
}

// ZookeeperProps holds workspace-level overrides for the Zookeeper container.
type ZookeeperProps struct {
	Image ImageRef `yaml:"image,omitempty"`
}

// OnlyOfficeProps holds workspace-level overrides for the OnlyOffice container.
type OnlyOfficeProps struct {
	Image       ImageRef `yaml:"image,omitempty"`
	MemoryLimit string   `yaml:"memoryLimit,omitempty"`
}

// PgAdminWsProps holds workspace-level overrides for the PgAdmin container.
type PgAdminWsProps struct {
	Image ImageRef `yaml:"image,omitempty"`
}

// AlfrescoProps holds workspace-level overrides for the Alfresco container.
type AlfrescoProps struct {
	Enabled bool     `yaml:"enabled,omitempty"`
	Aliases []string `yaml:"aliases,omitempty"`
}

// LicenseInstance represents a Citeck enterprise license.
type LicenseInstance struct {
	ID         string             `json:"id" yaml:"id"`
	Tenant     string             `json:"tenant" yaml:"tenant"`
	Priority   int64              `json:"priority" yaml:"priority"`
	IssuedTo   string             `json:"issuedTo" yaml:"issuedTo"`
	IssuedAt   string             `json:"issuedAt" yaml:"issuedAt"`
	ValidFrom  string             `json:"validFrom" yaml:"validFrom"`
	ValidUntil string             `json:"validUntil" yaml:"validUntil"`
	Content    any                `json:"content" yaml:"content"`
	Signatures []LicenseSignature `json:"signatures" yaml:"signatures"`
}

// LicenseSignature mirrors Kotlin's LicenseSignature record. Signature and
// Certificates are typed as `[]byte` so the downstream `json.Marshal` emits
// base64 strings (Go's default for `[]byte`), which is what eapps' Jackson
// expects for `byte[]` deserialization.
//
// YAML decoding requires the custom UnmarshalYAML below: yaml.v3 refuses to
// decode `!!binary` straight into `[]byte`, and when the target was `any` it
// stuffed the raw decoded bytes into a Go `string`. Marshaled to JSON, that
// string surfaced as raw binary peppered with U+FFFD replacement chars — and
// eapps' Jackson aborted with "Illegal character in base64 content" inside
// LicensesZkProviderInitializer, leaving eapps in a permanent "STARTING".
type LicenseSignature struct {
	Time         string   `json:"time"`
	Issuer       string   `json:"issuer"`
	Signature    []byte   `json:"signature"`
	Certificates [][]byte `json:"certificates"`
}

// UnmarshalYAML decodes a license signature from YAML, accepting both
// `!!binary` (the canonical workspace-v1.yml form, emitted by the Kotlin
// signing tool) and plain base64-encoded strings.
func (s *LicenseSignature) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Time         string      `yaml:"time"`
		Issuer       string      `yaml:"issuer"`
		Signature    yaml.Node   `yaml:"signature"`
		Certificates []yaml.Node `yaml:"certificates"`
	}
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("decode license signature: %w", err)
	}
	s.Time = raw.Time
	s.Issuer = raw.Issuer
	sig, err := decodeBinaryNode(&raw.Signature)
	if err != nil {
		return fmt.Errorf("decode signature bytes: %w", err)
	}
	s.Signature = sig
	s.Certificates = make([][]byte, 0, len(raw.Certificates))
	for i := range raw.Certificates {
		cert, err := decodeBinaryNode(&raw.Certificates[i])
		if err != nil {
			return fmt.Errorf("decode certificate[%d]: %w", i, err)
		}
		s.Certificates = append(s.Certificates, cert)
	}
	return nil
}

// decodeBinaryNode reads a scalar holding base64-encoded bytes. yaml.v3
// preserves the raw base64 literal in node.Value for `!!binary`-tagged scalars
// (it does not auto-decode), and an untagged scalar carries the same kind of
// payload from manually-base64'd workspace YAMLs. Either way we decode once.
func decodeBinaryNode(n *yaml.Node) ([]byte, error) {
	if n == nil || n.Value == "" {
		return nil, nil
	}
	// Strip newlines/whitespace introduced by block scalars (`|-`) so the
	// inner base64 is one contiguous string before decoding.
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, n.Value)
	decoded, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return decoded, nil
}

// SttSidecarProps configures the STT (speech-to-text) sidecar that proxies
// AI websocket traffic. When absent or empty, the generator falls back to the
// bundle's stt-sidecar app definition (image only). Port default 14080 keeps
// the sidecar inside the launcher's infrastructure cluster (away from
// 17020+ dynamic webapp ports). MemoryLimit default 2g matches the Kotlin
// reference (the gigaam model footprint).
type SttSidecarProps struct {
	Image       ImageRef `yaml:"image,omitempty"`
	MemoryLimit string   `yaml:"memoryLimit,omitempty"`
	Port        int      `yaml:"port,omitempty"`
}

// QdrantProps configures the Qdrant vector store that backs the rag webapp.
// There is deliberately no image key: the version is pinned by the bundle.
type QdrantProps struct {
	MemoryLimit string `yaml:"memoryLimit,omitempty"`
	GrpcPort    int    `yaml:"grpcPort,omitempty"`
}

// WorkspaceConfig is the top-level workspace-v1.yml structure.
type WorkspaceConfig struct {
	QuickStartVariants []QuickStartVariant `yaml:"quickStartVariants,omitempty"`
	Snapshots          []SnapshotDef       `yaml:"snapshots,omitempty"`
	NamespaceTemplates []NamespaceTemplate `yaml:"namespaceTemplates,omitempty"`
	ImageRepos         []ImageRepo         `yaml:"imageRepos"`
	BundleRepos        []BundlesRepo       `yaml:"bundleRepos,omitempty"`
	CiteckProxy        ProxyConfig         `yaml:"citeckProxy"`
	DefaultWebappProps WebappDefaultProps  `yaml:"defaultWebappProps,omitempty"`
	Webapps            []WebappConfig      `yaml:"webapps"`
	Postgres           PostgresProps       `yaml:"postgres,omitempty"`
	Keycloak           KeycloakProps       `yaml:"keycloak,omitempty"`
	Zookeeper          ZookeeperProps      `yaml:"zookeeper,omitempty"`
	OnlyOffice         OnlyOfficeProps     `yaml:"onlyoffice,omitempty"`
	PgAdmin            PgAdminWsProps      `yaml:"pgadmin,omitempty"`
	Alfresco           AlfrescoProps       `yaml:"alfresco,omitempty"`
	Licenses           []LicenseInstance   `yaml:"licenses,omitempty"`
	SttSidecar         *SttSidecarProps    `yaml:"sttSidecar,omitempty"`
	Qdrant             *QdrantProps        `yaml:"qdrant,omitempty"`
	// Dependencies is the workspace's `dependencies:` section: third-party
	// infrastructure images (postgres, rabbitmq, zookeeper, keycloak, mailpit,
	// pgadmin, onlyoffice…) named for every namespace of this workspace at
	// once. See DependencyEntry for why it exists beside the typed blocks
	// above rather than instead of them.
	Dependencies map[string]DependencyEntry `yaml:"dependencies,omitempty"`
	// AdditionalApps are custom containers added by configuration alone (no
	// dedicated launcher generator), defined once here and applied to every
	// namespace that uses this workspace. See AdditionalAppProps.
	AdditionalApps []AdditionalAppProps `yaml:"additionalApps,omitempty"`
	// Links are custom quick links shown in the launcher sidebar alongside the
	// built-in ones. See WorkspaceLink.
	Links []WorkspaceLink `yaml:"links,omitempty"`
}

// followAlias resolves a YAML alias node ("*anchor") to the node it points at.
//
// It matters because everything that reads an image reads it from the node tree
// rather than from a decoded map, and yaml.v3 hands an alias through verbatim
// when the destination is a yaml.Node — the alias check in its decoder sits
// AFTER the node short-circuit. An anchored image left unresolved would look
// exactly like an unreadable one, which for a dependency means staying on the
// pin and for an application means disappearing.
func followAlias(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return node
}

// decodeImageValues reads an `image:` value in any of the three shapes the
// launcher accepts and answers them as an ordered list.
//
// The shapes are the plain string ("postgres:18.6"), the {repository, tag}
// map every typed block and bundle entry uses, and a SEQUENCE of either. What
// the sequence MEANS is not decided here: the `dependencies:` section reads it
// as a ladder whose last rung is the target, and every other reader takes the
// first element (see the callers). One decoder rather than two, because the
// rule for reading a tag has drifted into two places before and the result was
// a spelling that worked in the bundle and failed in the workspace config.
//
// It decodes from the yaml.Node rather than from a generic map on purpose: in
// a map an unquoted `tag: 17.10` has already become float64(17.1), and the
// entry then names a version nobody wrote.
//
// A shape it cannot read answers nil, and so does a sequence with ONE
// unreadable element — the whole ladder, not just that rung. A ladder is a
// route, and a route with a hole in it is a hop the vendor was never asked
// about; the dependency staying on its pin is the only honest answer.
func decodeImageValues(node *yaml.Node) []string {
	node = followAlias(node)
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if v := strings.TrimSpace(node.Value); v != "" {
			return []string{v}
		}
		return nil
	case yaml.MappingNode:
		var pair struct {
			Repository string `yaml:"repository"`
			Tag        string `yaml:"tag"`
		}
		if err := node.Decode(&pair); err != nil {
			return nil
		}
		if pair.Repository == "" || pair.Tag == "" {
			return nil
		}
		return []string{pair.Repository + ":" + pair.Tag}
	case yaml.SequenceNode:
		out := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			one := decodeImageValues(item)
			if len(one) != 1 {
				// Either unreadable or itself a sequence. Both poison the
				// ladder: see the doc comment.
				return nil
			}
			out = append(out, one[0])
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

// ImageRef is an image reference in a TYPED config block. It accepts the same
// three shapes decodeImageValues does, and resolves a list to its FIRST
// element: a typed block is read by launchers with no dependency gate, so the
// only honest reading of a list there is the most conservative rung.
//
// It is a named string rather than a struct so that every existing reader
// stays a one-word conversion away, and so the value keeps marshaling back
// out as the plain string it always was.
type ImageRef string

// UnmarshalYAML decodes an ImageRef from any shape decodeImageValues knows,
// taking the first element of a list (see the type doc comment).
func (r *ImageRef) UnmarshalYAML(node *yaml.Node) error {
	values := decodeImageValues(node)
	if len(values) == 0 {
		*r = ""
		return nil
	}
	*r = ImageRef(values[0])
	return nil
}

// DependencyEntry is one entry of the workspace config's `dependencies:`
// section — an app id mapped to the image, or the LADDER of images, it should
// run.
//
// The section exists for ONE reason, and it is a compatibility contract with
// the workspace configs in the field rather than a matter of taste. It is the
// same reason the BUNDLE has one (see bundleDependenciesKey), one level up.
// This launcher pins each infra dependency to the version its DATA runs on and
// holds a breaking bump back until the operator runs a migration; NO older
// launcher does — the Kotlin 1.x line and every Go release up to 2.11.7 read
// `postgres.image`, `zookeeper.image`, `keycloak.image` and the rest of the
// typed blocks above and apply whatever they find straight onto the existing
// volume. A typed `postgres:` block raised from 17 to 18 therefore crash-loops
// an older launcher's stand, and a RabbitMQ 4.1 → 4.2 bump upgrades the Mnesia
// data in place, silently and irreversibly. Both parsers ignore keys they do
// not know, so an image moved in here is invisible to them: one workspace
// config can raise an infra version for 2.12+ users while everyone on an older
// launcher keeps running what they run today. The typed blocks stay for the
// mirror-image reason — an old launcher must keep seeing those.
//
// It is also useful in its own right: a version raised here applies to every
// namespace of the workspace, without waiting for a bundle to carry it.
//
// Images is the ladder as written. Image is its LAST rung, which is the
// target — everything that only wants "what should this run" reads Image and
// is unaffected by the ladder's existence.
type DependencyEntry struct {
	Image  string   `yaml:"-"`
	Images []string `yaml:"-"`
}

// UnmarshalYAML accepts any of the shapes decodeImageValues knows — the plain
// string ("postgres:17.11", the form the section was specified with), the
// {repository, tag} map every existing typed block and bundle entry uses, and
// a sequence of either, read as the ladder this entry names.
//
// It is deliberately TOTAL: it never reports an error. parseWorkspaceConfig
// drops the ENTIRE workspace config on a YAML error — imageRepos, webapps,
// bundleRepos and all — so a single entry this launcher cannot read must cost
// nothing but itself. That is the same judgement the bundle side makes about
// an id it does not know (parseBundleDependencies), and for the same reason:
// the section only works if what a reader does not understand is ignored.
func (d *DependencyEntry) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Image yaml.Node `yaml:"image"`
	}
	if err := node.Decode(&raw); err != nil {
		// Swallowed on purpose, not overlooked: see the doc comment — an entry
		// shape this launcher cannot read costs only itself, never the
		// workspace's whole config.
		return nil
	}
	values := decodeImageValues(&raw.Image)
	if len(values) == 0 {
		return nil
	}
	d.Images = values
	d.Image = values[len(values)-1]
	return nil
}

// DependencyImage answers the image the workspace's `dependencies:` section
// names for one app id, "" when it names none.
//
// The imageRepos rewriting happens HERE, at read time, and not while parsing:
// ResolveImageRef is a method on the config that owns ImageRepos, so the entry
// itself has no way to reach the registry map from inside UnmarshalYAML, and
// resolving on read keeps one rule in one place — the same one additionalApps
// and the bundle's own section already go through. A nil receiver answers
// nothing, so a generator with no workspace config stays correct.
func (w *WorkspaceConfig) DependencyImage(app string) string {
	if w == nil {
		return ""
	}
	return w.ResolveImageRef(w.Dependencies[app].Image)
}

// DependencyImageChain answers the LADDER the workspace's `dependencies:`
// section names for one app id, registry-resolved rung by rung, nil when it
// names none. A single-image entry answers a one-rung ladder, so callers need
// no second shape for the ordinary case.
func (w *WorkspaceConfig) DependencyImageChain(app string) []string {
	if w == nil {
		return nil
	}
	raw := w.Dependencies[app].Images
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, image := range raw {
		resolved := w.ResolveImageRef(image)
		if resolved == "" {
			return nil
		}
		out = append(out, resolved)
	}
	return out
}

// WorkspaceLink is a custom quick link declared in the workspace config and
// surfaced in the launcher sidebar. DependsOn gates its state against app
// runtime status: the link is hidden when any dependency is absent from the
// namespace, and disabled (shown, not clickable) when a present dependency is
// not RUNNING. With no dependencies the link is always enabled.
type WorkspaceLink struct {
	Name        string   `yaml:"name"`
	URL         string   `yaml:"url"`
	Icon        string   `yaml:"icon,omitempty"`        // /icons/<icon>.svg in the UI; else a generic link glyph
	Order       float64  `yaml:"order,omitempty"`       // sidebar sort key (built-ins use ~ -100..101)
	Category    string   `yaml:"category,omitempty"`    // grouping header (e.g. "Apps", "Resources")
	Description string   `yaml:"description,omitempty"` // hover tooltip
	DependsOn   []string `yaml:"dependsOn,omitempty"`   // app IDs this link depends on
}

// ImageReposByHost builds a map from registry host to ImageRepo for auth lookup.
func (w *WorkspaceConfig) ImageReposByHost() map[string]ImageRepo {
	m := make(map[string]ImageRepo)
	for _, repo := range w.ImageRepos {
		host := repo.URL
		if idx := strings.Index(host, "/"); idx > 0 {
			host = host[:idx]
		}
		m[host] = repo
	}
	return m
}

// TokenLookupFunc returns an auth token for a given repo auth type.
// Returns empty string if no credentials are available.
type TokenLookupFunc func(authType string) string

// WorkspaceRepoOpts carries the per-workspace git settings used when cloning
// the workspace repo (the one that owns workspace-v1.yml). Mirrors Kotlin's
// WorkspaceDto fields consumed by WorkspacesService.loadWorkspaceConfig —
// callers that don't supply this (CLI tools in server mode) fall through to
// the hardcoded DefaultBundlesRepo / DefaultBundlesBranch / defaultPullPeriod.
type WorkspaceRepoOpts struct {
	URL        string
	Branch     string
	PullPeriod time.Duration
	Token      string
}

// Resolver resolves bundle references to full bundle definitions.
type Resolver struct {
	dataDir     string
	tokenLookup TokenLookupFunc
	wsRepoOpts  *WorkspaceRepoOpts
	offline     bool         // skip all git operations, fail if local data missing
	forcePull   bool         // bypass the per-repo PullPeriod throttle (PullPeriod=0) for "Force Update"
	logger      *slog.Logger // nil → falls back to slog.Default()
	// wsOverlay, when set, transforms the RAW workspace-v1.yml bytes right
	// after they are read from disk and BEFORE parsing — the seam the daemon
	// uses to re-apply the user's manual workspace-config delta on top of the
	// git reference at every resolve. Living behind a plain func([]byte) keeps
	// the structural-merge engine (internal/namespace) out of bundle's imports.
	// Nil == today's behavior (git reference verbatim).
	wsOverlay func(raw []byte) ([]byte, error)
	// wsSyncErr records the LAST resolveWorkspace outcome when the workspace
	// repo git sync failed AND no usable workspace config could be loaded from
	// disk (the resolver fell back to an empty config). nil whenever any
	// config was loaded — a stale-but-present clone keeps things graceful.
	// Surfaced to callers via WorkspaceSyncError.
	wsSyncErr error
	// launcherVersion is this build's version, used ONLY to resolve LATEST to
	// the newest bundle this launcher can run. Empty (the default) keeps the
	// historical behavior: LATEST is the newest version, full stop.
	//
	// It is not a safety mechanism and must not become one — a construction
	// site that forgets WithLauncherVersion loses the convenience, never the
	// refusal. The refusal lives on the config WRITE paths, which do not go
	// through the resolver.
	launcherVersion string
}

// NewResolver creates a resolver without auth support.
func NewResolver(dataDir string) *Resolver {
	return &Resolver{dataDir: dataDir}
}

// NewResolverWithAuth creates a resolver with token lookup for authenticated repos.
func NewResolverWithAuth(dataDir string, tokenLookup TokenLookupFunc) *Resolver {
	return &Resolver{dataDir: dataDir, tokenLookup: tokenLookup}
}

// WithWorkspaceRepo configures the active workspace's git repo settings.
// When opts.URL is empty the resolver falls back to DefaultBundlesRepo so
// passing a partially populated struct (e.g. branch override only) still
// works. Chainable.
func (r *Resolver) WithWorkspaceRepo(opts WorkspaceRepoOpts) *Resolver {
	r.wsRepoOpts = &opts
	return r
}

// WithWorkspaceOverlay installs an overlay applied to the raw workspace-v1.yml
// bytes before parsing, in every workspace load path. Backs the manual
// workspace-config editing feature: the daemon passes a closure that loads the
// stored delta and re-applies it onto the freshly read git reference. A nil fn
// (the default) preserves the historical no-overlay behavior. Chainable.
func (r *Resolver) WithWorkspaceOverlay(fn func(raw []byte) ([]byte, error)) *Resolver {
	r.wsOverlay = fn
	return r
}

// WithLauncherVersion tells the resolver which launcher it is running inside,
// so LATEST can skip bundles that declare a higher minLauncherVersion.
// Chainable.
func (r *Resolver) WithLauncherVersion(v string) *Resolver {
	r.launcherVersion = v
	return r
}

// WithLogger sets the logger used for progress/warning messages. Chainable.
// Pass a quieter logger (e.g. WARN level) from CLI commands that want clean output,
// instead of mutating slog.Default() globally.
func (r *Resolver) WithLogger(logger *slog.Logger) *Resolver {
	r.logger = logger
	return r
}

// SetOffline enables offline mode: all git operations are skipped,
// and the resolver returns an error if required data is not available locally.
func (r *Resolver) SetOffline(offline bool) {
	r.offline = offline
}

// WithForcePull makes every workspace / bundle git sync ignore the per-repo
// PullPeriod throttle and pull unconditionally. Backs "Force Update [And
// Start]" (Kotlin 1.x parity: forceUpdate flips the git policy to REQUIRED).
// Chainable.
func (r *Resolver) WithForcePull() *Resolver {
	r.forcePull = true
	return r
}

// log returns the configured logger, or slog.Default() when none is set.
func (r *Resolver) log() *slog.Logger {
	if r.logger != nil {
		return r.logger
	}
	return slog.Default()
}

// ResolveResult contains the bundle definition and workspace config.
type ResolveResult struct {
	Bundle    *Def
	Workspace *WorkspaceConfig
}

// resolveWorkspace loads workspace config from local repo/ dir or clones the default workspace repo.
// Returns (config, repoDir) where repoDir is the directory the config was loaded from.
func (r *Resolver) resolveWorkspace() (cfg *WorkspaceConfig, repoDir string) {
	localRepoDir := filepath.Join(r.dataDir, "repo")
	defaultRepoDir := filepath.Join(r.dataDir, "bundles", "workspace")

	// Reset the recorded sync error — each resolveWorkspace call reflects only
	// its own outcome (a later successful pull clears an earlier failure).
	r.wsSyncErr = nil

	// A repo/ that is itself a git clone (has .git) is a STALE managed clone
	// left behind by an older launcher — current code only ever extracts a
	// workspace ZIP here (no .git). It must not shadow the git-pulled
	// bundles/workspace, or versions added by auto-pull / Force Update (e.g. a
	// new "2026.2" bundle) would never appear. A genuine offline ZIP import
	// (no .git) keeps top priority and stays hands-off (never pulled).
	repoIsManagedClone := dirIsGitClone(localRepoDir)

	// Observability only (priorities below are unchanged): a legacy managed
	// clone whose origin disagrees with the effective workspace URL is the
	// fingerprint of a workspace repo URL lost in migration.
	if repoIsManagedClone {
		effectiveURL, _, _, _ := r.workspaceRepoSettings()
		r.warnOnLegacyRepoOriginMismatch(localRepoDir, effectiveURL)
	}

	// Priority 1: manual / offline ZIP import (repo/ without .git).
	if !repoIsManagedClone {
		if wsCfg := r.loadWorkspaceConfigOverlaid(localRepoDir); wsCfg != nil {
			return wsCfg, localRepoDir
		}
	}

	// Priority 2: cloned workspace repo (git pull if online)
	var syncErr error
	if !r.offline {
		repoURL, repoBranch, pullPeriod, token := r.workspaceRepoSettings()
		gitCtx, gitCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := git.CloneOrPullWithAuth(gitCtx, git.RepoOpts{
			URL: repoURL, Branch: repoBranch, Token: token,
			DestDir: defaultRepoDir, PullPeriod: pullPeriod,
		})
		gitCancel()
		if err != nil {
			r.log().Warn("Failed to sync workspace repo", "err", err)
			// Keep the underlying git error text intact (%w) — callers and the
			// Web UI heuristic match on "authentication required" /
			// "repository not found" wording from go-git.
			syncErr = fmt.Errorf("sync workspace repo %s: %w", repoURL, err)
		}
	}
	if wsCfg := r.loadWorkspaceConfigOverlaid(defaultRepoDir); wsCfg != nil {
		return wsCfg, defaultRepoDir
	}

	// Priority 3: last-resort fallback to a stale managed clone in repo/ when
	// nothing else loaded (e.g. offline with no bundles/workspace clone yet).
	if repoIsManagedClone {
		if wsCfg := r.loadWorkspaceConfigOverlaid(localRepoDir); wsCfg != nil {
			return wsCfg, localRepoDir
		}
	}
	// Nothing loaded: record the sync failure (if any) so callers can surface
	// it instead of silently treating the empty fallback as a real workspace.
	r.wsSyncErr = syncErr
	return &WorkspaceConfig{}, ""
}

// WorkspaceSyncError returns the workspace-repo git sync error recorded by the
// most recent resolveWorkspace pass, but only when ALL of the following hold:
//
//   - the resolver targets a CUSTOM workspace repo URL (WithWorkspaceRepo with
//     a non-empty URL) — failures of the default Citeck repo keep the historical
//     graceful empty-config fallback;
//   - the git sync (clone/pull) failed;
//   - no usable workspace config could be loaded from disk afterwards (no ZIP
//     import, no previously-cloned copy, no stale managed clone).
//
// Callers (daemon Welcome-data endpoints, workspace switch) use this to fail
// loudly with the repo URL + underlying git error instead of silently serving
// the built-in fallback workspace (Kotlin 1.x parity: workspace load failed
// hard with a retryable auth prompt — never a silent empty workspace).
//
// On the empty-URL gate below (deliberate, do not "fix" by widening it): an
// empty configured URL means the DEFAULT Citeck workspace is in play, and
// hard-failing every default-repo sync error here would break server bootstrap
// on a fresh/offline box and turn the daemon's Welcome-data endpoints into 502s
// (see routes_workspace.go / server.go, and TestWorkspaceSyncError_DefaultRepoStaysGraceful).
// The real damage an empty URL causes — the launcher silently serving a
// workspace the user never configured — is instead made visible where it
// actually bites and where the truth is known: unknownBundleRepoError (which
// says so in the user-facing message AND logs a one-shot WARN) and
// warnOnLegacyRepoOriginMismatch (which reports the surviving repo/.git origin).
func (r *Resolver) WorkspaceSyncError() error {
	if r.wsRepoOpts == nil || r.wsRepoOpts.URL == "" {
		return nil
	}
	return r.wsSyncErr
}

// WorkspaceSyncErrorAny returns the recorded sync error for ANY workspace repo,
// including the built-in Citeck default that WorkspaceSyncError deliberately
// stays quiet about.
//
// The quiet default is right for booting — the daemon must come up without
// reaching github.com — but not for deciding whether a resulting EMPTY config
// is normal. A user whose network reaches gitlab.citeck.ru but not github.com
// got a `default` workspace with no bundleRepos and no error anywhere: the
// create dialog's bundle-repository dropdown was empty and unfillable, and
// Quick Start produced a namespace with an empty bundle ref — seven
// third-party containers reporting RUNNING with none of the product in them.
// Callers pair this with "and the config is unusable"; see
// workspaceSyncErrorString.
func (r *Resolver) WorkspaceSyncErrorAny() error {
	return r.wsSyncErr
}

// workspaceRepoSettings resolves the URL/branch/pullPeriod/token to use for
// the workspace repo clone, layering WithWorkspaceRepo overrides on top of
// the hardcoded defaults. Empty override fields keep the corresponding
// default — this lets a workspace ship with only a custom branch and still
// inherit the canonical Citeck workspace URL.
func (r *Resolver) workspaceRepoSettings() (url, branch string, pullPeriod time.Duration, token string) {
	url, branch, pullPeriod = DefaultBundlesRepo, DefaultBundlesBranch, defaultPullPeriod
	if r.wsRepoOpts != nil {
		if r.wsRepoOpts.URL != "" {
			url = r.wsRepoOpts.URL
		}
		if r.wsRepoOpts.Branch != "" {
			branch = r.wsRepoOpts.Branch
		}
		if r.wsRepoOpts.PullPeriod > 0 {
			pullPeriod = r.wsRepoOpts.PullPeriod
		}
		token = r.wsRepoOpts.Token
	}
	if r.forcePull {
		pullPeriod = 0 // Force Update: bypass throttle, pull unconditionally
	}
	return url, branch, pullPeriod, token
}

// ResolveWorkspaceOnly loads workspace config without resolving a bundle.
// Used by the daemon to provide workspace data (templates, quick starts, bundle repos)
// even when no namespace is configured yet (e.g. fresh server before wizard).
func (r *Resolver) ResolveWorkspaceOnly() *WorkspaceConfig {
	wsCfg, _ := r.resolveWorkspace()
	return wsCfg
}

// Resolve fetches and parses a bundle definition along with workspace config.
func (r *Resolver) Resolve(ref Ref) (*ResolveResult, error) {
	if ref.IsEmpty() {
		// A namespace with no bundle ref is a broken config, not a reason to
		// forget the workspace. Returning a blank WorkspaceConfig here was
		// SILENTLY REPLACING the caller's real one — the daemon assigns it
		// verbatim (`a.workspaceConfig = resolveResult.Workspace`) — leaving it
		// with no bundleRepos, no imageRepos and no namespace templates. The
		// next namespace created then got an empty bundle ref too (the
		// applyDefaultTemplate fallback needs BundleRepos), the secrets start
		// gate saw no auth-required registries, and registry auth resolved
		// nothing: one broken namespace poisoned the launcher. Note the
		// resolve-FAILURE path in namespace_loader.go preserves the workspace
		// for precisely this reason; this branch was the one that did not.
		wsCfg, _ := r.resolveWorkspace()
		return &ResolveResult{Bundle: &EmptyDef, Workspace: wsCfg}, nil
	}

	wsCfg, wsRepoDir := r.resolveWorkspace()

	// Step 2: Resolve the actual repo URL for ref.Repo from workspace config
	bundleRepo := findBundleRepo(wsCfg, ref.Repo)
	if bundleRepo == nil {
		// HARD failure. This used to fall through to syncBundleRepo with a nil
		// entry, which defaulted the URL and cloned the canonical Citeck
		// workspace into bundles/<unknown-id> — the launcher then served a
		// workspace nobody configured, with no warning at all.
		return nil, r.unknownBundleRepoError(wsCfg, ref.Repo)
	}

	localBundles := shouldUseLocalBundles(wsRepoDir, bundleRepo)

	var repoDir string
	if localBundles {
		repoDir = wsRepoDir
		r.log().Debug("Using workspace repo for bundles (local)", "repo", ref.Repo, "dir", wsRepoDir)
	} else {
		repoDir = r.syncBundleRepo(ref.Repo, *bundleRepo)
	}

	// Build alias → canonical name map
	aliasMap := buildAliasMap(wsCfg)
	imageRepoMap := buildImageRepoMap(wsCfg)

	// Resolve bundle version — look in BundlesRepo.Path sub-directory if defined
	bundlesDir := repoDir
	if bundleRepo != nil && bundleRepo.Path != "" {
		bundlesDir = filepath.Join(repoDir, bundleRepo.Path)
	}
	key := ref.Key
	if strings.EqualFold(key, "LATEST") {
		latest, latestErr := LatestRunnableBundle(bundlesDir, r.launcherVersion, r.log())
		if latestErr != nil {
			return nil, latestErr
		}
		key = latest
	}

	bundlePath := findBundleFile(bundlesDir, key)
	if bundlePath == "" {
		return nil, fmt.Errorf("bundle %s not found in %s", key, bundlesDir)
	}

	def, err := parseBundleFile(bundlePath, key, aliasMap, imageRepoMap, r.log())
	if err != nil {
		return nil, err
	}
	return &ResolveResult{Bundle: def, Workspace: wsCfg}, nil
}

// shouldUseLocalBundles checks if bundle files should be read from the workspace dir
// instead of cloning a separate git repo. Returns true when:
//   - bundleRepo.URL is empty (explicit "use workspace repo"), OR
//   - workspace dir already contains the bundle path on disk
//     (covers downloaded zip archives where bundleRepo.URL points to the source repo).
func shouldUseLocalBundles(wsRepoDir string, bundleRepo *BundlesRepo) bool {
	if wsRepoDir == "" || bundleRepo == nil {
		return false
	}
	if bundleRepo.URL == "" {
		return true
	}
	bundlePath := wsRepoDir
	if bundleRepo.Path != "" {
		bundlePath = filepath.Join(wsRepoDir, bundleRepo.Path)
	}
	info, err := os.Stat(bundlePath)
	return err == nil && info.IsDir()
}

// bundleRepoGitSettings resolves the git URL/branch for a DECLARED bundle repo
// entry. Empty per-field values inherit the canonical Citeck defaults — a
// documented, tested feature (a workspace may declare a repo with only a
// branch, or with no git coordinates at all when its bundles ship inside the
// workspace repo itself). It is only an UNKNOWN repo id — no entry at all —
// that must never be defaulted; see unknownBundleRepoError.
func bundleRepoGitSettings(bundleRepo BundlesRepo) (url, branch string) {
	url, branch = DefaultBundlesRepo, DefaultBundlesBranch
	if bundleRepo.URL != "" {
		url = bundleRepo.URL
	}
	if bundleRepo.Branch != "" {
		branch = bundleRepo.Branch
	}
	return url, branch
}

// syncBundleRepo clones or pulls the bundle git repository and returns its local directory.
//
// bundleRepo is taken BY VALUE on purpose: it must be an entry that actually
// exists in the workspace config. The previous *BundlesRepo signature allowed a
// nil (= repo id not found in the config) to reach here and silently clone the
// DEFAULT workspace repo into bundles/<unknown-id>, so the launcher served a
// workspace the user never configured. Callers now reject an unknown id first
// (Resolve / SyncBundleRepo → unknownBundleRepoError).
func (r *Resolver) syncBundleRepo(repoID string, bundleRepo BundlesRepo) string {
	repoDir := filepath.Join(r.dataDir, "bundles", repoID)
	repoURL, repoBranch := bundleRepoGitSettings(bundleRepo)
	repoToken := r.lookupRepoToken(&bundleRepo)

	if !r.offline {
		gitCtx, gitCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := git.CloneOrPullWithAuth(gitCtx, git.RepoOpts{
			URL: repoURL, Branch: repoBranch, DestDir: repoDir,
			Token: repoToken, PullPeriod: r.bundleRepoPullPeriod(&bundleRepo),
		})
		gitCancel()
		if err != nil {
			r.log().Warn("Failed to sync bundle repo", "repo", repoID, "err", err)
		}
	}
	return repoDir
}

// bundleRepoPullPeriod resolves the git pull throttle for a bundle repo: the
// repo's configured PullPeriod (falling back to the default), or zero when
// forcePull is set ("Force Update" — bypass the throttle, pull unconditionally).
func (r *Resolver) bundleRepoPullPeriod(bundleRepo *BundlesRepo) time.Duration {
	if r.forcePull {
		return 0
	}
	if bundleRepo != nil && bundleRepo.PullPeriod != "" {
		if d, ok := parsePullPeriod(bundleRepo.PullPeriod); ok {
			return d
		}
	}
	return defaultPullPeriod
}

// lookupRepoToken returns an auth token for the given bundle repo using the token lookup func.
func (r *Resolver) lookupRepoToken(bundleRepo *BundlesRepo) string {
	if r.tokenLookup == nil {
		return ""
	}
	var token string
	if bundleRepo.AuthType != "" {
		token = r.tokenLookup(bundleRepo.AuthType)
	}
	// Fallback: try GIT_TOKEN type (covers Kotlin-migrated secrets with scope ws:{wsId}:repo)
	if token == "" {
		token = r.tokenLookup("GIT_TOKEN")
	}
	return token
}

// loadRawWorkspaceConfig returns the raw bytes of the first existing workspace
// config file in repoDir (workspace-v1.yml priority order), the path it was
// read from, and whether one was found. These are the PRE-overlay bytes — the
// pristine git reference — used both for parsing and as the editor baseline.
func loadRawWorkspaceConfig(repoDir string) (raw []byte, path string, ok bool) {
	candidates := []string{"workspace-v1.yml", "workspace-v1.yaml", "workspace.yml"}
	for _, name := range candidates {
		p := filepath.Join(repoDir, name)
		data, err := os.ReadFile(p) //nolint:gosec // path is constructed from fixed filenames within repoDir
		if err != nil {
			continue
		}
		return data, p, true
	}
	return nil, "", false
}

// parseWorkspaceConfig unmarshals raw workspace-v1.yml bytes, logging (never
// failing hard on) a parse error so callers fall through to the next priority.
func parseWorkspaceConfig(raw []byte, path string, logger *slog.Logger) *WorkspaceConfig {
	if logger == nil {
		logger = slog.Default()
	}
	var cfg WorkspaceConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		logger.Warn("Failed to parse workspace config", "path", path, "err", err)
		return nil
	}
	// Validate additionalApps non-fatally: a malformed entry in the shared workspace
	// config must not block resolution for everyone. Drop the whole section and log
	// so the maintainer sees it; the rest of the workspace config still applies.
	if err := ValidateAdditionalApps(cfg.AdditionalApps); err != nil {
		logger.Warn("Invalid additionalApps in workspace config; ignoring them", "path", path, "err", err)
		cfg.AdditionalApps = nil
	}
	// A dependencies entry whose image could not be read (see
	// DependencyEntry.UnmarshalYAML, which is total on purpose) costs only
	// itself — but not silently: unreported, a typo leaves the stand on the
	// launcher's own default with no line anywhere explaining it.
	for name, dep := range cfg.Dependencies {
		if dep.Image == "" {
			logger.Warn("Workspace dependency entry names no image; ignoring it", "app", name, "path", path)
		}
	}
	return &cfg
}

// loadWorkspaceConfig reads + parses the workspace config from repoDir with NO
// overlay (the pristine git reference). Kept as a package function for tests and
// callers that explicitly want the un-overlaid config.
func loadWorkspaceConfig(repoDir string, logger *slog.Logger) *WorkspaceConfig {
	raw, path, ok := loadRawWorkspaceConfig(repoDir)
	if !ok {
		return nil
	}
	return parseWorkspaceConfig(raw, path, logger)
}

// loadWorkspaceConfigOverlaid reads the raw workspace config, applies the
// configured wsOverlay (the user's manual delta) when set, then parses. An
// overlay error is logged and the pristine git reference is parsed instead — a
// broken delta never blocks workspace resolution.
func (r *Resolver) loadWorkspaceConfigOverlaid(repoDir string) *WorkspaceConfig {
	raw, path, ok := loadRawWorkspaceConfig(repoDir)
	if !ok {
		return nil
	}
	if r.wsOverlay != nil {
		if merged, err := r.wsOverlay(raw); err != nil {
			r.log().Warn("Workspace config overlay failed; using git reference", "path", path, "err", err)
		} else {
			raw = merged
		}
	}
	return parseWorkspaceConfig(raw, path, r.log())
}

// ResolveWorkspaceRaw resolves the workspace repo (git-syncing exactly as the
// normal resolve does) and returns the RAW workspace-v1.yml bytes BEFORE any
// overlay, plus the dir they came from. The workspace-config editor uses this
// to compute the git baseline the user's delta is layered on; build the
// resolver WITHOUT an overlay so the returned bytes are the pristine reference.
//
// It re-reads the winning file once after resolveWorkspace already parsed it —
// one extra os.ReadFile of a single small YAML, only on editor open. That's a
// deliberate trade for keeping resolveWorkspace's priority-chain return shape
// unchanged (it yields the parsed config + dir, not raw bytes); do not "optimize"
// by collapsing the two without threading raw bytes through every priority branch.
func (r *Resolver) ResolveWorkspaceRaw() (raw []byte, repoDir string) {
	_, dir := r.resolveWorkspace()
	if dir == "" {
		return nil, ""
	}
	raw, _, _ = loadRawWorkspaceConfig(dir)
	return raw, dir
}

// FindSnapshot finds a SnapshotDef by ID in the workspace config.
func FindSnapshot(cfg *WorkspaceConfig, snapshotID string) *SnapshotDef {
	if cfg == nil {
		return nil
	}
	for i := range cfg.Snapshots {
		if cfg.Snapshots[i].ID == snapshotID {
			return &cfg.Snapshots[i]
		}
	}
	return nil
}

// SyncBundleRepo clones or pulls a single bundle repo (by ID) declared in cfg,
// returning its local bundles directory. Honors WithForcePull — used by the
// namespace edit dialog's per-repo refresh to fetch a repo whose versions
// aren't on disk yet (e.g. release/alf-develop when only develop was resolved).
func (r *Resolver) SyncBundleRepo(cfg *WorkspaceConfig, repoID string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("no workspace config")
	}
	repo := findBundleRepo(cfg, repoID)
	if repo == nil {
		// The Web UI renders this string verbatim in a toast
		// (NamespaceEditDialog), so the enrichment lands end-to-end with no
		// frontend change.
		return "", r.unknownBundleRepoError(cfg, repoID)
	}
	return r.syncBundleRepo(repoID, *repo), nil
}

// unknownBundleRepoError builds the diagnostic for a bundle repo id that the
// loaded workspace config does not declare — the single failure message for
// both entry points (Resolve and SyncBundleRepo).
//
// It must be actionable on its own, because it is what the user sees: the Web
// UI prints it verbatim in a toast and the daemon surfaces it as the namespace
// bundle error. So it names (a) the unknown id, (b) the ids that ARE declared,
// and (c) the EFFECTIVE workspace repo URL + branch the declarations were read
// from — the three facts needed to tell "typo in the ref" apart from "the
// launcher is reading the wrong workspace".
//
// When the effective URL is the built-in default only because the configured
// one is empty, that is called out explicitly: it is the actual user-facing
// cause of the incident this diagnostic was written for (a 1.x→2.x migration
// blanked the workspace's repo_url, so every repo id in every namespace became
// "unknown" against the default Citeck workspace).
func (r *Resolver) unknownBundleRepoError(cfg *WorkspaceConfig, repoID string) error {
	ids := knownBundleRepoIDs(cfg)
	// An empty config has two very different causes. Only one of them is the
	// user's config; the other is a workspace-repo sync outage (network/auth),
	// after which nothing was ever read from disk. Blaming "declares no bundle
	// repos" there sends the user to fix a file that was never the problem, so
	// when the resolver holds the real git error, say that instead.
	syncFailed := len(ids) == 0 && r.wsSyncErr != nil
	known := "the workspace config declares no bundle repos"
	switch {
	case len(ids) > 0:
		known = "known ids: " + strings.Join(ids, ", ")
	case syncFailed:
		known = "the workspace repo could not be synced, so no workspace config was loaded at all"
	}
	url, branch, _, _ := r.workspaceRepoSettings()

	// Name the actual YAML key: without it the reader has to already know the
	// schema to act on this error.
	msg := fmt.Sprintf("bundle repo %q is not declared under bundleRepos: in workspace-v1.yml (%s); "+
		"workspace repo: %s (branch %s)", repoID, known, url, branch)
	if syncFailed {
		// Rendered verbatim in a Web UI toast: lead with what to do, then the
		// underlying git error (its wording — "authentication required",
		// "repository not found" — is what actually tells the user which).
		msg += "; fix workspace repo access and retry — this is a sync failure, not a wrong repo id: " +
			r.wsSyncErr.Error()
	}
	if r.workspaceURLDefaultedFromEmpty() {
		msg += "; the configured workspace repo URL is empty, so the default Citeck workspace " +
			"is being used — restore the workspace's repository URL (a 1.x→2.x migration can blank it)"
		// Also make it visible in the daemon log, not only in the caller's
		// error: this is the narrowest way to satisfy "empty URL + unknown
		// bundle repo must not be silent" without touching WorkspaceSyncError
		// (see the note there) — it fires only on this already-failing path.
		warnOnce("empty-ws-url|"+r.dataDir+"|"+repoID, func() {
			r.log().Warn("Namespace references an unknown bundle repo while the workspace repo URL is empty "+
				"— the default Citeck workspace is being served",
				"repo", repoID, "effectiveUrl", url, "effectiveBranch", branch)
		})
	}
	return errors.New(msg)
}

// knownBundleRepoIDs lists the bundle repo ids declared in cfg, in declaration
// order (the order the user sees in the workspace file and the UI picker).
func knownBundleRepoIDs(cfg *WorkspaceConfig) []string {
	if cfg == nil {
		return nil
	}
	ids := make([]string, 0, len(cfg.BundleRepos))
	for i := range cfg.BundleRepos {
		if id := cfg.BundleRepos[i].ID; id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// workspaceURLDefaultedFromEmpty reports whether the resolver is falling back
// to DefaultBundlesRepo because a workspace record EXISTS but carries an empty
// URL (the migration-damage fingerprint). A nil wsRepoOpts is "no per-workspace
// override configured" — legitimately the default, not damage.
//
// Desktop-gated, and that is not incidental. In SERVER mode FileStore
// synthesizes a single workspace ("daemon") that never carries a repo URL, so
// buildWorkspaceRepoOpts always yields a non-nil opts with URL == "" and this
// would be permanently true — decorating every unknown-bundle-repo error with
// advice to "restore the workspace's repository URL" that names a 1.x→2.x
// migration which, per bootstrap.go, cannot even run outside desktop mode.
// Server mode having no workspace repo record is the design, not damage.
func (r *Resolver) workspaceURLDefaultedFromEmpty() bool {
	return config.IsDesktopMode() && r.wsRepoOpts != nil && r.wsRepoOpts.URL == ""
}

// findBundleRepo finds a BundlesRepo entry by ID in the workspace config.
func findBundleRepo(cfg *WorkspaceConfig, repoID string) *BundlesRepo {
	for i := range cfg.BundleRepos {
		if cfg.BundleRepos[i].ID == repoID {
			return &cfg.BundleRepos[i]
		}
	}
	return nil
}

func buildAliasMap(cfg *WorkspaceConfig) map[string]string {
	m := make(map[string]string)
	for _, app := range cfg.Webapps {
		for _, alias := range app.Aliases {
			m[alias] = app.ID
		}
	}
	for _, alias := range cfg.CiteckProxy.Aliases {
		m[alias] = "proxy"
	}
	for _, alias := range cfg.Alfresco.Aliases {
		m[alias] = "alfresco"
	}
	return m
}

func buildImageRepoMap(cfg *WorkspaceConfig) map[string]string {
	m := make(map[string]string)
	for _, repo := range cfg.ImageRepos {
		m[repo.ID] = repo.URL
	}
	return m
}

// ResolveImageRef rewrites a bundle-style single-string image reference whose
// first path segment is an imageRepos ID — e.g. "core/ecos-model:1.1-SNAPSHOT"
// with imageRepos[core].url=nexus.citeck.ru → "nexus.citeck.ru/ecos-model:1.1-SNAPSHOT".
// The tag (":1.1") and/or digest ("@sha256:…") are preserved. A reference that
// does NOT start with a known repo ID — a full registry ref ("registry.x/y:1"),
// a Docker Hub image ("busybox:1.36"), or a bare local tag ("edi-sim:0.1.0") —
// is returned verbatim. A nil receiver returns the input unchanged so callers
// without a workspace config (CLI tools, tests) stay correct.
func (w *WorkspaceConfig) ResolveImageRef(image string) string {
	if w == nil {
		return strings.TrimSpace(image)
	}
	// Reuse the single imageRepos ID→URL mapping (same source as
	// resolveImageURL's repository+tag-pair call site) instead of re-scanning
	// ImageRepos here.
	return resolveImageRefWithRepos(image, buildImageRepoMap(w))
}

// resolveImageRefWithRepos is ResolveImageRef's body expressed over the
// already-built imageRepos ID→URL map. parseBundleFile is handed that map and
// never sees a WorkspaceConfig, so this is what lets the `dependencies:`
// section's plain-string image form go through exactly the same rewriting as
// every other bundle image instead of a second, drifting copy of the rule.
func resolveImageRefWithRepos(image string, imageRepoMap map[string]string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return image
	}
	repository, suffix := image, ""
	// Strip an "@sha256:…" digest first so its ':' isn't mistaken for a tag.
	if at := strings.LastIndexByte(repository, '@'); at >= 0 {
		suffix = repository[at:]
		repository = repository[:at]
	}
	// A ':' belongs to a tag only when it sits after the last '/'. A ':' before
	// the last '/' is a registry host:port (already a full ref, left alone).
	if i := strings.LastIndexByte(repository, ':'); i >= 0 && !strings.ContainsRune(repository[i:], '/') {
		suffix = repository[i:] + suffix
		repository = repository[:i]
	}
	prefix, rest, ok := strings.Cut(repository, "/")
	if !ok {
		return image // no prefix segment → nothing to map (e.g. "busybox")
	}
	if registryURL, found := imageRepoMap[prefix]; found {
		return registryURL + "/" + rest + suffix
	}
	return image
}

func parseBundleFile(path, version string, aliasMap, imageRepoMap map[string]string, logger *slog.Logger) (*Def, error) {
	if logger == nil {
		logger = slog.Default()
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from internal bundle dir
	if err != nil {
		return nil, fmt.Errorf("read bundle %s: %w", version, err)
	}

	// Parse as generic map — this is what Def.Content carries to every reader
	// that wants the bundle's own shape (ecos: scope included).
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse bundle %s: %w", version, err)
	}

	// Parse AGAIN, as a node tree, and read every image from THAT. In the
	// generic map an unquoted `tag: 17.10` is already float64(17.1): the tag
	// failed a string type-assertion, came back empty, and the whole
	// application vanished from the bundle — no image, no container, no log
	// line. The `dependencies:` section has read its tags from the YAML text
	// since it was introduced; this is the same rule for everything else.
	var rootNode yaml.Node
	if err := yaml.Unmarshal(data, &rootNode); err != nil {
		return nil, fmt.Errorf("parse bundle %s: %w", version, err)
	}

	applications := make(map[string]AppDef)
	dependencies := parseBundleDependencies(data, imageRepoMap, logger)
	var citeckApps []AppDef
	minLauncherVersion := parseBundleMinLauncherVersion(documentRoot(&rootNode))

	// processApp handles one bundle entry. When appName is "ecos", it recurses
	// into sub-entries (Helm charts group core apps under an ecos: key).
	var processApp func(appName string, entry *yaml.Node)
	processApp = func(appName string, entry *yaml.Node) {
		if appName == "" {
			return
		}
		if appName == "ecos" {
			// Recurse into nested entries under ecos: scope
			for _, sub := range mappingEntries(entry) {
				processApp(sub.key, sub.node)
			}
			return
		}

		image, named := extractBundleImage(entry, imageRepoMap)
		if image == "" {
			// An entry with no `image:` at all is ordinary — a bundle carries
			// plenty of keys that are not applications. An entry that NAMES an
			// image the launcher cannot read is not: the application then
			// silently leaves the bundle, and the stand comes up missing it
			// with nothing anywhere saying why. That is the same price the
			// `dependencies:` section refuses to pay one level down.
			if named {
				logger.Warn("Bundle entry names an image the launcher cannot read; ignoring the application",
					"app", appName)
			}
			return
		}

		// Map alias to canonical name
		canonical := appName
		if mapped, ok := aliasMap[appName]; ok {
			canonical = mapped
		}
		applications[canonical] = AppDef{Image: image}

		// Collect citeck apps (ecos-apps init containers). Kotlin gates this on
		// the eapps app (+ aliases) only — ecosAppsImages on any other app is
		// ignored — so match that to avoid leaking unrelated init images into
		// eapps' init containers.
		if canonical == appdef.AppEapps {
			citeckApps = collectCiteckApps(entry, imageRepoMap, citeckApps)
		}
	}

	for _, top := range mappingEntries(documentRoot(&rootNode)) {
		appName := top.key
		// Neither of these is an application. `dependencies` MUST be skipped
		// (an entry id colliding with the entry schema's own key would be read
		// as an app named "dependencies"); `minLauncherVersion` is a scalar and
		// would be ignored anyway — it is named here so the non-application
		// keys are one list rather than an inference from a type switch.
		if appName == bundleDependenciesKey || appName == bundleMinLauncherVersionKey {
			continue
		}
		processApp(appName, top.node)
	}

	def := &Def{
		Key:                Key{Version: version},
		Applications:       applications,
		Dependencies:       dependencies,
		CiteckApps:         citeckApps,
		MinLauncherVersion: minLauncherVersion,
		Content:            raw,
	}

	logger.Debug("Resolved bundle", "version", version,
		"apps", len(applications), "dependencies", len(dependencies), "citeckApps", len(citeckApps))
	return def, nil
}

// bundleDependenciesKey is the top-level bundle key carrying third-party
// infrastructure images.
//
// It exists for ONE reason, and it is a compatibility contract with the
// bundles in the field rather than a matter of taste. This launcher pins each
// infra dependency to the version its DATA runs on and holds a breaking bundle
// bump back until the operator runs a migration; NO older launcher does — the
// Kotlin 1.x line and every Go release up to 2.11.7 apply whatever image the
// bundle names straight onto the existing volume. A top-level `postgres:` entry
// raised from 17 to 18 therefore crash-loops an older launcher's stand ("The
// data directory was initialized by PostgreSQL version 17…"), and a RabbitMQ
// 4.1 → 4.2 bump upgrades the Mnesia data in place, silently and irreversibly.
// Both parsers ignore keys they do not know, so an image moved in here is
// invisible to them: one bundle can raise an infra version for 2.12+ users
// while everyone on an older launcher keeps running what they run today.
// Citeck apps stay at the top level for exactly the mirror-image reason — an
// old launcher must keep seeing those.
const bundleDependenciesKey = "dependencies"

// bundleMinLauncherVersionKey is the top-level bundle key carrying the launcher
// floor. Named here rather than inlined so the list of top-level keys that are
// NOT applications is readable in one place (the other is
// bundleDependenciesKey).
const bundleMinLauncherVersionKey = "minLauncherVersion"

// parseBundleMinLauncherVersion reads the floor from the YAML text.
//
// From the text, not from the generic map, for the same reason the image tags
// are: `minLauncherVersion: 2.10` is a YAML float, and a map decode hands back
// 2.1 — a floor the author never wrote, one patch release too low, silently.
//
// A shape that is not a scalar answers "" and costs the key alone. The bundle
// keeps its applications: a launcher that cannot read the floor is in no worse
// a position than one that predates the key entirely.
func parseBundleMinLauncherVersion(root *yaml.Node) string {
	node := followAlias(root)
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for _, e := range mappingEntries(node) {
		if e.key != bundleMinLauncherVersionKey {
			continue
		}
		v := followAlias(e.node)
		if v == nil || v.Kind != yaml.ScalarNode {
			return ""
		}
		return strings.TrimSpace(v.Value)
	}
	return ""
}

// parseBundleDependencies reads the `dependencies:` section, from the YAML
// itself rather than from the generic map the rest of parseBundleFile walks.
// That is not a style choice: in the generic map an unquoted `tag: 17.11` has
// already become float64(17.11), from which "17.10" comes back as "17.1", and
// the entry silently names a version nobody wrote. Decoding the section through
// the SAME DependencyEntry the workspace config uses keeps the tag's raw text
// and guarantees one spelling cannot work in one file and fail in the other.
// Each entry names its `image:` in either of the two forms — the plain string
// ("postgres:17.11", the spelling the section was specified with) or the
// {repository, tag} map every existing top-level entry uses — both resolved
// through the same imageRepos rewriting.
//
// Keys are canonical launcher app ids (postgres, rabbitmq, mailpit…) and are
// NOT alias-mapped: the alias map describes Citeck webapps, and infra has no
// aliases. An id this launcher does not know is kept and simply never asked
// for — a future launcher may know it, and failing the whole bundle over it
// would defeat the point of a section older launchers are meant to ignore.
// An entry whose image cannot be read is SKIPPED and logged. Skipping is what
// makes the section safe to write at all, but silence is the wrong price for a
// typo: the author's version then simply does not apply, the stand keeps
// running the launcher's own default, and nothing anywhere says why.
func parseBundleDependencies(data []byte, imageRepoMap map[string]string, logger *slog.Logger) map[string]AppDef {
	if logger == nil {
		logger = slog.Default()
	}
	var doc struct {
		Dependencies map[string]DependencyEntry `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// The bundle as a whole already parsed above, so this can only be the
		// section's own shape. It costs the section, never the bundle.
		logger.Warn("Cannot read the bundle's dependencies section; ignoring it", "err", err)
		return nil
	}
	out := make(map[string]AppDef, len(doc.Dependencies))
	for name, entry := range doc.Dependencies {
		images := make([]string, 0, len(entry.Images))
		for _, raw := range entry.Images {
			image := resolveImageRefWithRepos(raw, imageRepoMap)
			if image == "" {
				images = nil
				break
			}
			images = append(images, image)
		}
		if len(images) == 0 {
			logger.Warn("Bundle dependency entry names no image; ignoring it", "app", name)
			continue
		}
		out[name] = AppDef{Image: images[len(images)-1], Images: images}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// collectCiteckApps extracts ecos-apps init container images from a bundle entry.
//
// Each element names its image in any shape decodeImageValues knows, and a LIST
// element resolves to its FIRST rung for the same reason every other reader
// outside `dependencies:` does — an init container has no pin, no hold and no
// migration, so the most conservative rung is the only honest reading.
func collectCiteckApps(entry *yaml.Node, imageRepoMap map[string]string, citeckApps []AppDef) []AppDef {
	var raw struct {
		EcosAppsImages []yaml.Node `yaml:"ecosAppsImages"`
	}
	if !decodeEntry(entry, &raw) {
		return citeckApps
	}
	for i := range raw.EcosAppsImages {
		values := decodeImageValues(&raw.EcosAppsImages[i])
		if len(values) == 0 {
			continue
		}
		if citeckAppImage := resolveImageRefWithRepos(values[0], imageRepoMap); citeckAppImage != "" {
			citeckApps = append(citeckApps, AppDef{Image: citeckAppImage})
		}
	}
	return citeckApps
}

// extractBundleImage extracts one image URL from a bundle entry's `image:`.
//
// A LIST is accepted and its FIRST element taken. Nothing outside the
// `dependencies:` section can walk a ladder — there is no pin, no hold and no
// migration out here — so the only honest reading is the most conservative
// rung, which is the same rule LegacyImage() follows.
// It answers whether the entry NAMED an image at all alongside the image
// itself, so that a name the launcher cannot read can be told apart from a key
// that is simply not an application.
func extractBundleImage(entry *yaml.Node, imageRepoMap map[string]string) (image string, named bool) {
	var raw struct {
		Image yaml.Node `yaml:"image"`
	}
	if !decodeEntry(entry, &raw) {
		return "", false
	}
	if raw.Image.IsZero() {
		return "", false
	}
	values := decodeImageValues(&raw.Image)
	if len(values) == 0 {
		return "", true
	}
	return resolveImageRefWithRepos(values[0], imageRepoMap), true
}

// decodeEntry decodes one bundle entry's mapping node into out, answering
// whether anything usable came back.
//
// Decoding rather than hand-walking node.Content is what makes YAML's merge key
// ("<<: *base") keep working: generated bundles inherit an `image:` that way,
// and the decoder applies the merge where a manual walk would see only the
// literal keys.
func decodeEntry(entry *yaml.Node, out any) bool {
	entry = followAlias(entry)
	if entry == nil || entry.Kind != yaml.MappingNode {
		return false
	}
	return entry.Decode(out) == nil
}

// yamlEntry is one key→value pair of a bundle mapping, in document order.
type yamlEntry struct {
	key  string
	node *yaml.Node
}

// documentRoot unwraps the document node yaml.Unmarshal produces.
func documentRoot(node *yaml.Node) *yaml.Node {
	node = followAlias(node)
	if node != nil && node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		node = followAlias(node.Content[0])
	}
	return node
}

// mappingEntries lists a mapping node's entries in DOCUMENT order, resolving
// aliased values. Document order rather than map order because two bundle keys
// can alias-map onto one canonical app id, and which of them wins must not
// depend on Go's map iteration.
//
// A merge key ("<<") is expanded too, so an app list inherited from an anchor
// is listed like any other. The precedence is YAML's own: a key written in the
// mapping itself beats the same key coming in through a merge, wherever the
// merge key sits, and among several merged mappings the first one wins. Merged
// entries are listed after the explicit ones.
func mappingEntries(node *yaml.Node) []yamlEntry {
	node = followAlias(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]yamlEntry, 0, len(node.Content)/2)
	at := make(map[string]int, len(node.Content)/2)
	var merges []*yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Tag == mergeTag {
			merges = append(merges, mergedMappings(node.Content[i+1])...)
			continue
		}
		if key.Kind != yaml.ScalarNode {
			continue
		}
		entry := yamlEntry{key: key.Value, node: followAlias(node.Content[i+1])}
		if idx, dup := at[key.Value]; dup {
			// A duplicated key keeps its place and takes the LAST value, which
			// is what decoding the same mapping into a map would have done.
			out[idx] = entry
			continue
		}
		at[key.Value] = len(out)
		out = append(out, entry)
	}
	for _, merged := range merges {
		for _, e := range mappingEntries(merged) {
			if _, taken := at[e.key]; taken {
				continue
			}
			at[e.key] = len(out)
			out = append(out, e)
		}
	}
	return out
}

// mergeTag is the tag yaml.v3 resolves the "<<" merge key to.
const mergeTag = "!!merge"

// mergedMappings answers the mappings a "<<" value brings in. YAML allows a
// single mapping or a sequence of them, earlier entries winning.
func mergedMappings(value *yaml.Node) []*yaml.Node {
	value = followAlias(value)
	if value == nil {
		return nil
	}
	if value.Kind == yaml.SequenceNode {
		out := make([]*yaml.Node, 0, len(value.Content))
		for _, item := range value.Content {
			if m := followAlias(item); m != nil && m.Kind == yaml.MappingNode {
				out = append(out, m)
			}
		}
		return out
	}
	if value.Kind == yaml.MappingNode {
		return []*yaml.Node{value}
	}
	return nil
}

// findBundleFile resolves a bundle key (e.g. "2025.10" or "archive/2025.5")
// to a YAML file path on disk. Two layout flavors are supported:
//
//  1. Flat layout — `<key>.yml` / `<key>.yaml` or `<key>/values.{yml,yaml}`
//     at the top level. Cheap O(1) stat lookup.
//  2. Nested Helm-chart layout — bundles live arbitrarily deep under the
//     bundles dir, with values.yml at sub-dir leaves. Walks the tree and
//     consults the same key→path map that ListBundleVersions builds.
//
// Falls back to the flat lookup first for backwards compatibility and to
// preserve the old preference order (.yaml > .yml > .../values.yaml > etc.).
// The nested walk only runs when the flat lookup misses — bundle dirs with
// thousands of files don't pay the walk cost on every lookup.
func findBundleFile(dir, key string) string {
	// Flat layout: keep the existing candidate order for backwards
	// compatibility (matters for the .yaml > .yml preference test).
	flatCandidates := []string{
		filepath.Join(dir, key+".yaml"),
		filepath.Join(dir, key+".yml"),
		filepath.Join(dir, key, "values.yaml"),
		filepath.Join(dir, key, "values.yml"),
		filepath.Join(dir, key),
	}
	for _, path := range flatCandidates {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path
		}
	}

	// Nested layout: walk the tree and look up by the full Kotlin-style key.
	// Slash-normalized so the same key works on Windows path separators.
	normalized := filepath.ToSlash(key)
	for entry := range walkBundles(dir) {
		if entry.Key == normalized {
			return entry.Path
		}
	}
	return ""
}

// bundleEntry is one bundle definition discovered by walkBundles. Key uses
// forward slashes regardless of OS so it can be compared verbatim with the
// keys carried in bundle.Ref / quick-start variants.
type bundleEntry struct {
	Key  string // e.g. "2025.10" or "archive/2025.5"
	Path string // absolute path to the YAML file
}

// walkBundles yields every bundle YAML file under root, computing the
// Kotlin-equivalent key per file. Matches BundleUtils.loadKitsFiles semantics:
//
//   - Recurses into all subdirectories.
//   - For `.yml`/`.yaml` files, the key is the file's path relative to root,
//     with the extension stripped and OS separators normalized to `/`.
//   - When the filename is exactly `values.yml` or `values.yaml`, the key
//     drops the filename — the parent dir's relative path becomes the key.
//     This lets a Helm-chart `archive/2025.5/values.yml` resolve as key
//     `archive/2025.5` exactly like Kotlin.
//   - Returns the entries as an iter.Seq so callers can short-circuit (e.g.
//     `findBundleFile` stops at the first match). For repos with thousands
//     of bundles the lazy traversal avoids a full materialized list.
//
// Errors during walk are intentionally swallowed — bundle resolution is
// best-effort and a single unreadable file should not poison the whole
// lookup. The caller already handles the "no matching key" outcome.
func walkBundles(root string) func(yield func(bundleEntry) bool) {
	return func(yield func(bundleEntry) bool) {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				// Skip unreadable subtree but keep walking siblings.
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
				return nil
			}

			// Key derivation mirrors Kotlin BundleUtils.loadKitsFiles:
			//   - `values.{yml,yaml}` → drop filename, use parent dir's rel path
			//   - any other file       → strip extension from the file's rel path
			stem := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
			var keyPath string
			if stem == "values" {
				// values.yml at the root would yield key="" which is invalid;
				// skip such files (Kotlin doesn't reach this branch either —
				// loadBundles starts at the bundle root).
				dir := filepath.Dir(path)
				rel, err := filepath.Rel(root, dir)
				if err != nil || rel == "." || rel == "" {
					return nil
				}
				keyPath = rel
			} else {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return nil
				}
				// Strip the .yml/.yaml from the relative path's tail.
				ext := filepath.Ext(rel)
				keyPath = strings.TrimSuffix(rel, ext)
			}

			key := filepath.ToSlash(keyPath)
			if !yield(bundleEntry{Key: key, Path: path}) {
				return errSeqStop
			}
			return nil
		})
	}
}

// errSeqStop is a sentinel returned from inside filepath.WalkDir when the
// iterator's yield callback has signaled stop. Carries no semantic meaning
// other than "halt the walk"; the caller in walkBundles ignores all errors.
var errSeqStop = errors.New("walk stopped by iterator")

// isVersionString checks if a name looks like a version (starts with a digit).
// Citeck bundle versions follow the format "YYYY.N" (e.g. "2025.10"), so all valid
// version strings start with a digit. This filters out non-version files like README.yml.
func isVersionString(name string) bool {
	return name != "" && name[0] >= '0' && name[0] <= '9'
}

// VersionEntry represents a bundle version with its repo and key.
type VersionEntry struct {
	Repo    string
	Key     string
	Ref     string // "repo:key"
	Current bool
}

// ResolveBundleRepoDir returns the on-disk directory for a bundle repo.
// Priority: 1) offline import (data/repo/{path}), 2) local workspace repo,
// 3) cloned repo (data/bundles/{repoID}/).
// Exported so the daemon API can reuse the same resolution logic.
func ResolveBundleRepoDir(dataDir, wsRepoDir string, repo BundlesRepo) string {
	// A repo/ that is a git clone (has .git) is a stale managed clone from an
	// older launcher (see resolveWorkspace) — it must not shadow the freshly
	// pulled workspace repo, or new bundle versions never show up. Only a
	// manual ZIP import (no .git) keeps offline-import priority.
	offlineImportIsStaleClone := dirIsGitClone(filepath.Join(dataDir, "repo"))

	if repo.Path != "" && !offlineImportIsStaleClone {
		// Priority 1: offline ZIP import
		localRepo := filepath.Join(dataDir, "repo", repo.Path)
		if info, err := os.Stat(localRepo); err == nil && info.IsDir() {
			return localRepo
		}
	}
	// Priority 2: local workspace repo
	if shouldUseLocalBundles(wsRepoDir, &repo) {
		if repo.Path != "" {
			return filepath.Join(wsRepoDir, repo.Path)
		}
		return wsRepoDir
	}
	// Priority 3: cloned repo
	dir := filepath.Join(dataDir, "bundles", repo.ID)
	if repo.Path != "" {
		dir = filepath.Join(dir, repo.Path)
	}
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	// Priority 4: last-resort fallback to a stale managed clone in repo/ when
	// no other layout resolved (keeps offline air-gapped clones working).
	if repo.Path != "" && offlineImportIsStaleClone {
		localRepo := filepath.Join(dataDir, "repo", repo.Path)
		if info, err := os.Stat(localRepo); err == nil && info.IsDir() {
			return localRepo
		}
	}
	return dir
}

// warnedOnce dedupes one-shot diagnostics. It is package-level on purpose: the
// daemon builds a FRESH Resolver per request, so a per-Resolver flag would
// still print on every API call. Keys are self-describing strings; the set is
// bounded by the number of distinct workspaces times the number of diagnostics.
var warnedOnce sync.Map // map[string]struct{}

// warnOnce invokes emit only the first time key is seen in this process.
func warnOnce(key string, emit func()) {
	if _, loaded := warnedOnce.LoadOrStore(key, struct{}{}); !loaded {
		emit()
	}
}

// (Reading a clone's origin remote and comparing two git URLs both live in
// internal/git — git.OriginURL and git.SameRepoURL. Local copies of BOTH used
// to exist here. The comparator disagreed outright: it lowercased the WHOLE
// URL, while git.SameRepoURL folds only scheme+host and treats path case as
// significant, because git hosts are case-sensitive in the path. Two different
// answers to "is this the same repo?" is a trap; there is now one of each.)

// warnOnLegacyRepoOriginMismatch emits a one-shot WARN when data/repo/ is a git
// clone whose origin remote disagrees with the effective workspace repo URL.
//
// Why this exists: repo/.git's origin is the highest-fidelity surviving record
// of the workspace URL the user actually configured, and nothing else reads it.
// When a 1.x→2.x migration loses the stored repo_url, workspaceRepoSettings
// silently falls back to the DEFAULT Citeck workspace and the launcher serves a
// workspace the user never asked for — a mismatch here is that situation's
// fingerprint. Diagnostic ONLY: the priority chain is unchanged (see the
// rationale at the top of resolveWorkspace), and recovering the stored setting
// belongs to the migrator (internal/h2migrate), not to the resolver.
func (r *Resolver) warnOnLegacyRepoOriginMismatch(localRepoDir, effectiveURL string) {
	// Best-effort: a legacy clone too broken for go-git to open, or one with no
	// origin remote, is simply "no origin" — this diagnostic must never fail
	// loudly on the path that resolves every namespace.
	origin, err := git.OriginURL(localRepoDir)
	if err != nil || origin == "" || git.SameRepoURL(origin, effectiveURL) {
		return
	}
	warnOnce("legacy-repo-origin|"+localRepoDir+"|"+origin+"|"+effectiveURL, func() {
		r.log().Warn("Legacy workspace clone in repo/ points at a different git remote "+
			"than the configured workspace repo — the workspace repo URL may have been lost "+
			"(e.g. by a 1.x→2.x migration)",
			"repoDir", localRepoDir, "originUrl", origin, "effectiveUrl", effectiveURL)
	})
}

// dirIsGitClone reports whether dir is a git working clone (has a .git entry).
// Used to tell a stale *managed* clone left in the offline-import location
// (repo/) apart from a genuine offline workspace ZIP import (which has no .git
// and must keep top priority).
func dirIsGitClone(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// ListAllVersions lists all bundle versions across all configured repos,
// correctly resolving local workspace paths vs cloned repo paths.
func (r *Resolver) ListAllVersions(currentRef string) []VersionEntry {
	wsCfg, wsRepoDir := r.resolveWorkspace()

	var entries []VersionEntry
	for _, repo := range wsCfg.BundleRepos {
		bundlesDir := ResolveBundleRepoDir(r.dataDir, wsRepoDir, repo)
		for _, v := range ListBundleVersions(bundlesDir) {
			ref := repo.ID + ":" + v
			entries = append(entries, VersionEntry{
				Repo:    repo.ID,
				Key:     v,
				Ref:     ref,
				Current: ref == currentRef,
			})
		}
	}
	return entries
}

// ListBundleVersions lists available bundle version keys in a given bundles
// sub-directory, recursing into nested layouts (Helm charts that group
// bundles under sub-dirs with values.yml). Returns keys like "2025.10" for
// flat files and "archive/2025.5" for nested ones, sorted descending so the
// first element is the highest-priority candidate (matches Kotlin
// `BundleUtils.loadBundles` which returns a TreeMap sorted by BundleKey).
//
// Only keys that look like a bundle version are returned — paths whose final
// segment doesn't start with a digit (README.yml etc.) are filtered, since
// the existing `isVersionString` check is the cheapest way to weed out
// non-bundle YAMLs the walker picks up.
func ListBundleVersions(bundlesDir string) []string {
	if _, err := os.Stat(bundlesDir); err != nil {
		return nil
	}
	var versions []string
	for entry := range walkBundles(bundlesDir) {
		// The "version-ness" check is applied to the final path segment so
		// keys like "archive/2025.5" are accepted (final = "2025.5") while
		// "archive/README" is filtered out (final = "README").
		final := entry.Key
		if idx := strings.LastIndex(final, "/"); idx >= 0 {
			final = final[idx+1:]
		}
		if !isVersionString(final) {
			continue
		}
		versions = append(versions, entry.Key)
	}
	// Sort newest first — callers expect versions[0] to be the latest.
	slices.SortFunc(versions, func(a, b string) int {
		return compareBundleVersions(b, a) // descending
	})
	return versions
}

// compareBundleVersions compares version strings matching Kotlin BundleKey.compareTo:
// 1. Parse scope (path before last '/'), version parts, and suffix
// 2. Compare scope (prefer no scope), then version parts, then suffix parts
// 3. Trailing zeros stripped from version (2025.12.0 == 2025.12)
// 4. No suffix > has suffix (2025.12 > 2025.12-beta1)
// 5. Suffix groups: digits+dots as version numbers, strings lexicographically
func compareBundleVersions(a, b string) int {
	ak, bk := parseBundleKey(a), parseBundleKey(b)

	// 1. Compare scope: prefer empty (no-scope ranks higher)
	if c := compareStringSlices(ak.scope, bk.scope, true); c != 0 {
		return c
	}
	// 2. Compare version parts numerically
	if c := compareIntSlices(ak.versionParts, bk.versionParts, false); c != 0 {
		return c
	}
	// 3. Compare suffix parts: prefer empty (release > pre-release)
	return compareSuffixParts(ak.suffixParts, bk.suffixParts)
}

// bundleKey mirrors Kotlin BundleKey: scope + versionParts + suffixParts.
type bundleKey struct {
	scope        []string
	versionParts []int
	suffixParts  []any // string or bundleKey (recursive)
}

// parseBundleKey parses "archive/2025.5-RC1.1" into scope=["archive"], version=[2025,5], suffix=["RC", key("1.1")].
func parseBundleKey(raw string) bundleKey {
	key := raw

	// Extract scope (everything before the last '/')
	var scope []string
	if idx := strings.LastIndex(key, "/"); idx != -1 {
		scopeStr := key[:idx]
		for s := range strings.SplitSeq(scopeStr, "/") {
			if s != "" {
				scope = append(scope, s)
			}
		}
		key = key[idx+1:]
	}

	// Split version prefix from suffix: version = leading digits+dots
	firstNonVersion := strings.IndexFunc(key, func(r rune) bool {
		return r != '.' && (r < '0' || r > '9')
	})
	versionStr := key
	suffixStr := ""
	if firstNonVersion != -1 {
		versionStr = key[:firstNonVersion]
		suffixStr = key[firstNonVersion:]
		// Strip leading separator
		if suffixStr != "" && !isLetterOrDigit(rune(suffixStr[0])) {
			suffixStr = suffixStr[1:]
		}
	}

	// Parse version parts, strip trailing zeros
	var versionParts []int
	for p := range strings.SplitSeq(versionStr, ".") {
		if n, err := strconv.Atoi(p); err == nil {
			versionParts = append(versionParts, n)
		}
	}
	for len(versionParts) > 0 && versionParts[len(versionParts)-1] == 0 {
		versionParts = versionParts[:len(versionParts)-1]
	}

	// Parse suffix parts: split by digit-or-dot vs letter groups (Kotlin splitByGroups)
	var suffixParts []any
	if suffixStr != "" {
		for _, group := range splitSuffixGroups(suffixStr) {
			if group != "" && isDigitOrDot(rune(group[0])) {
				suffixParts = append(suffixParts, parseBundleKey(group)) // recursive
			} else {
				suffixParts = append(suffixParts, group)
			}
		}
	}

	return bundleKey{scope: scope, versionParts: versionParts, suffixParts: suffixParts}
}

func isLetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isDigitOrDot(r rune) bool {
	return r == '.' || (r >= '0' && r <= '9')
}

// splitSuffixGroups splits suffix by character type: digit-or-dot vs letter.
// "RC1.1" → ["RC", "1.1"], "beta2" → ["beta", "2"].
// Matches Kotlin StringUtils.splitByGroups with predicate: digit/dot → group 1, else → group 0.
func splitSuffixGroups(s string) []string {
	if s == "" {
		return nil
	}
	var groups []string
	start := 0
	for i := 1; i < len(s); i++ {
		prevType := isDigitOrDot(rune(s[i-1]))
		curType := isDigitOrDot(rune(s[i]))
		if prevType != curType {
			groups = append(groups, s[start:i])
			start = i
		}
	}
	groups = append(groups, s[start:])
	return groups
}

// compareIntSlices compares int slices. preferEmpty: true means shorter/empty wins
// (used for scope — no scope is better), false means longer wins (used for version parts).
func compareIntSlices(a, b []int, preferEmpty bool) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] > b[i] {
			return 1
		} else if a[i] < b[i] {
			return -1
		}
	}
	if len(a) == len(b) {
		return 0
	}
	if preferEmpty {
		if len(a) < len(b) {
			return 1
		}
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return -1
}

// compareStringSlices compares string slices with preferEmpty semantics.
func compareStringSlices(a, b []string, preferEmpty bool) int {
	n := min(len(a), len(b))
	for i := range n {
		if c := strings.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	if len(a) == len(b) {
		return 0
	}
	if preferEmpty {
		if len(a) < len(b) {
			return 1
		}
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return -1
}

// compareSuffixParts compares parsed suffix part lists (any = string or bundleKey).
// Empty suffix list is preferred (release > pre-release).
func compareSuffixParts(a, b []any) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	// Empty suffix > non-empty (release > pre-release)
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	n := min(len(a), len(b))
	for i := range n {
		c := compareSuffixPart(a[i], b[i])
		if c != 0 {
			return c
		}
	}
	// Fewer parts = better (prefer shorter suffix)
	if len(a) < len(b) {
		return 1
	} else if len(a) > len(b) {
		return -1
	}
	return 0
}

func compareSuffixPart(a, b any) int {
	aKey, aIsKey := a.(bundleKey)
	bKey, bIsKey := b.(bundleKey)
	aStr, aIsStr := a.(string)
	bStr, bIsStr := b.(string)

	switch {
	case aIsKey && bIsKey:
		if c := compareIntSlices(aKey.versionParts, bKey.versionParts, false); c != 0 {
			return c
		}
		return compareSuffixParts(aKey.suffixParts, bKey.suffixParts)
	case aIsStr && bIsStr:
		return strings.Compare(aStr, bStr)
	default:
		// bundleKey > string
		if aIsKey {
			return 1
		}
		return -1
	}
}
