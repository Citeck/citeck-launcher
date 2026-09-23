package bundle

import (
	"fmt"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"gopkg.in/yaml.v3"
)

// The TYPE of an additionalApps entry. An entry with no type is the raw
// container this section has always carried — image, env, cmd, volumes, probes
// — and the launcher only runs it. A typed entry is something the launcher
// KNOWS: it is registered in the dependency registry, so it gets a version pin,
// a generation-counted volume, a major-version migration and a rollback, and in
// exchange the launcher owns the fields that make those work (the data volume,
// the data layout, the image precedence).
//
// The type therefore decides which settings the entry carries: each one is read
// as its own DTO and nothing else. Keys that belong to another shape are simply
// not read — an entry is a declaration, not a script, and refusing a workspace
// config over a stray key would break stands for a typo.
const (
	AppTypeContainer = ""         // raw container (historical shape)
	AppTypePostgres  = "POSTGRES" // a PostgreSQL cluster beside the stand's own
	AppTypeQdrant    = "QDRANT"   // a Qdrant vector store
)

// PostgresAppProps is the DTO behind `type: POSTGRES` — ONE PostgreSQL cluster
// a namespace runs beside the stand's own database, the observer's today and
// another service's tomorrow.
//
// Whether the cluster EXISTS is not decided here: like qdrant and the observer
// itself, it exists exactly where the bundle names its image. This entry only
// says how it is configured, so a stand whose release does not ship the service
// does not get its database either, with no switch to keep in sync.
//
// Every field but Name is optional; the defaults are the ones the official
// postgres image itself applies, so the smallest usable entry is a name plus
// the type.
type PostgresAppProps struct {
	// Name is the dependency id AND the container name — `citeck deps` and
	// `citeck stop <name>` both use it. Naming a cluster the launcher already
	// knows (observer-postgres) overrides ITS settings field by field.
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	// Enabled defaults to true; false keeps the declaration without running it.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Image is the fallback this launcher runs when nothing higher in the
	// precedence chain names one. A BUNDLE's `dependencies:` entry for the same
	// id outranks it, which is what lets a release move the version.
	Image ImageRef `yaml:"image,omitempty"`
	// User and DB are POSTGRES_USER and POSTGRES_DB; DB defaults to User and
	// User defaults to "postgres", as the image itself does. They are not
	// cosmetic: the migration plan addresses the cluster AS this user and
	// tolerates exactly the role and database the image creates from them.
	User string `yaml:"user,omitempty"`
	DB   string `yaml:"db,omitempty"`
	// Password is POSTGRES_PASSWORD, and is meant to be a reference —
	// `${secret:<id>}` — to the namespace's own value, seeded from the
	// workspace `secrets:` defaults; a literal works too. Empty defaults to the
	// user's name, what these internal, unpublished clusters have always used.
	// A reference with no value keeps the cluster from being generated at all
	// (and with it everything that depends on it): a database initialized with
	// an empty password would keep it.
	Password string `yaml:"password,omitempty"`
	// Port is published on the host in DESKTOP mode, so the service that owns
	// this database can be run outside the launcher against it. 0 publishes
	// nothing. Server mode publishes nothing but the proxy either way, for
	// every app alike — see Generate.
	Port int `yaml:"port,omitempty"`
	// MemoryLimit is the container limit ("512m"); empty takes the launcher's
	// default for a side database.
	MemoryLimit string `yaml:"memoryLimit,omitempty"`
	// VolumeBase is the stem of the data volume name, without the generation
	// suffix: "obs_postgres" gives obs_postgres2, obs_postgres3, … Empty
	// derives it from the name, and it must never change afterwards — the
	// volume name IS how the data is found.
	VolumeBase string `yaml:"volumeBase,omitempty"`
	// Settings are server settings passed as `-c key=value`, in KEY ORDER, so
	// the generated command — and therefore the deployment hash — is stable.
	Settings map[string]string `yaml:"settings,omitempty"`
}

// QdrantAppProps is the DTO behind `type: QDRANT` — a vector store instance.
// Same rule as PostgreSQL: the bundle's image decides that it exists, this
// entry decides how it is configured, and naming the built-in store (qdrant)
// overrides its settings field by field.
type QdrantAppProps struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Enabled     *bool    `yaml:"enabled,omitempty"`
	Image       ImageRef `yaml:"image,omitempty"`
	MemoryLimit string   `yaml:"memoryLimit,omitempty"`
	// HTTPPort and GrpcPort are published in desktop mode (6333 / 6334 for the
	// built-in store), so a service indexing into it can run outside the
	// launcher. 0 publishes nothing.
	HTTPPort   int    `yaml:"httpPort,omitempty"`
	GrpcPort   int    `yaml:"grpcPort,omitempty"`
	VolumeBase string `yaml:"volumeBase,omitempty"`
}

// AdditionalAppProps declares a custom container to run in a namespace alongside
// the built-in Citeck/infra apps, with no dedicated launcher generator. It lives
// in the workspace config (workspace-v1.yml `additionalApps:`) so a service is
// defined once and distributed to every namespace that uses the workspace — applied
// live on each generation, exactly like `webapps:`.
type AdditionalAppProps struct {
	// Name is the container/app name (unique; must not collide with a built-in app).
	Name string `yaml:"name" json:"name"`
	// Type selects which DTO the entry is read as: empty for the raw container
	// described below, POSTGRES or QDRANT for a dependency the launcher knows.
	// See the AppType constants.
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
	// Postgres and Qdrant hold the typed entry's settings. Exactly one is set,
	// and only when Type says so; the raw container fields below are then
	// unused. A typed entry never reaches generateAdditionalApps.
	Postgres *PostgresAppProps `yaml:"-" json:"postgres,omitempty"`
	Qdrant   *QdrantAppProps   `yaml:"-" json:"qdrant,omitempty"`
	// Enabled defaults to true; set false to keep the definition but not deploy it.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Image is the full Docker image reference to run (registry/repo:tag, or a
	// locally-present tag), or a bundle-style "<repoId>/path:tag" resolved through
	// the workspace imageRepos. Pulled like any other app; registry auth comes from
	// the workspace imageRepos by host match.
	//
	// EMPTY means the BUNDLE names it — under this entry's name, in either of
	// its sections — and a bundle that does not name one gets no container. The
	// same rule a typed entry follows: whether a service exists is the
	// release's answer, and the workspace only says how it is configured.
	Image string `yaml:"image,omitempty" json:"image,omitempty"`
	// Kind classifies the app (CITECK_CORE / CITECK_CORE_EXTENSION / CITECK_ADDITIONAL
	// / THIRD_PARTY); empty defaults to THIRD_PARTY.
	Kind              string                    `yaml:"kind,omitempty" json:"kind,omitempty"`
	NetworkAliases    []string                  `yaml:"networkAliases,omitempty" json:"networkAliases,omitempty"`
	Environments      map[string]string         `yaml:"environments,omitempty" json:"environments,omitempty"`
	Cmd               []string                  `yaml:"cmd,omitempty" json:"cmd,omitempty"`
	Ports             []string                  `yaml:"ports,omitempty" json:"ports,omitempty"`
	Volumes           []string                  `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	DependsOn         []string                  `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`
	StartupConditions []appdef.StartupCondition `yaml:"startupConditions,omitempty" json:"startupConditions,omitempty"`
	LivenessProbe     *appdef.AppProbeDef       `yaml:"livenessProbe,omitempty" json:"livenessProbe,omitempty"`
	Resources         *appdef.AppResourcesDef   `yaml:"resources,omitempty" json:"resources,omitempty"`
	ShmSize           string                    `yaml:"shmSize,omitempty" json:"shmSize,omitempty"`
	// InitContainers run to completion before the main container starts (a
	// wait-for, a schema migration, a fixture loader). Each is a full
	// InitContainerDef (image + env + volumes + cmd); ${VAR} is resolved in env
	// and cmd just like the main container.
	InitContainers []appdef.InitContainerDef `yaml:"initContainers,omitempty" json:"initContainers,omitempty"`
	// InitActions are exec commands run inside the container right after it is
	// created (e.g. createbucket, a one-off CLI call). ${VAR} is resolved in args.
	InitActions []appdef.AppInitAction `yaml:"initActions,omitempty" json:"initActions,omitempty"`
	// StopTimeout is the per-app graceful-stop budget in seconds (SIGTERM→SIGKILL
	// window); 0 falls back to the daemon default.
	StopTimeout int `yaml:"stopTimeout,omitempty" json:"stopTimeout,omitempty"`
	// CloudConfig is what the desktop CloudConfigServer hands this service when
	// it is stopped here and run from an IDE instead: flat keys in the service's
	// own configuration syntax. Strings take the same ${VAR} / ${secret:<id>}
	// substitution as env; numbers, booleans, lists and maps pass through.
	CloudConfig map[string]any `yaml:"cloudConfig,omitempty" json:"cloudConfig,omitempty"`
}

// IsEnabled reports whether the additional app should be deployed (default true).
func (a AdditionalAppProps) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

// reservedAppNames are the built-in infra/core container names an additional app
// must not reuse (reusing one would override that built-in app's definition). This
// is the static fast-reject list; collisions with bundle-loaded webapp ids (edi,
// integrations, …) are caught at generation time where the full app set is known.
var reservedAppNames = map[string]bool{
	appdef.AppProxy: true, appdef.AppGateway: true, appdef.AppEapps: true,
	appdef.AppEmodel: true, appdef.AppUiserv: true, appdef.AppHistory: true,
	appdef.AppNotifications: true, appdef.AppTransformations: true, appdef.AppEproc: true,
	appdef.AppPostgres: true, appdef.AppZookeeper: true, appdef.AppRabbitmq: true,
	appdef.AppMongodb: true, appdef.AppMailpit: true, appdef.AppKeycloak: true,
	appdef.AppPgadmin: true, appdef.AppOnlyoffice: true, appdef.AppAlfresco: true,
	appdef.AppAlfPostgres: true, appdef.AppAlfSolr: true,
	appdef.AppContent: true, appdef.AppAi: true,
	appdef.AppSttSidecar: true,
}

// overridableTypedNames are the built-in dependencies a TYPED entry may name:
// doing so configures that cluster rather than colliding with it (the settings
// are merged onto the launcher's own defaults, field by field).
//
// The stand's own `postgres` is deliberately absent. It carries a volume name
// every launcher ever shipped agrees on, an init script per webapp datasource
// and a pin seeded from real field data; generateInfra owns it, and a workspace
// entry that looked like it configured it would be describing none of that.
var overridableTypedNames = map[string]string{
	appdef.AppQdrant: AppTypeQdrant,
}

// ValidateAdditionalApps checks each additional app has a name, that names are
// unique, and that they do not collide with a reserved built-in container name.
// No entry needs an image: without one, the bundle names it or the entry is not
// generated (see AdditionalAppProps.Image).
func ValidateAdditionalApps(apps []AdditionalAppProps) error {
	seen := make(map[string]bool, len(apps))
	for i, a := range apps {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return fmt.Errorf("additionalApps[%d]: name is required", i)
		}
		if reservedAppNames[name] && (!a.IsTyped() || overridableTypedNames[name] != a.Type) {
			return fmt.Errorf("additionalApps[%q]: name collides with a built-in app; choose another", name)
		}
		if seen[name] {
			return fmt.Errorf("additionalApps[%q]: duplicate name", name)
		}
		seen[name] = true
		for j, ic := range a.InitContainers {
			if strings.TrimSpace(ic.Image) == "" {
				return fmt.Errorf("additionalApps[%q].initContainers[%d]: image is required", name, j)
			}
		}
		if a.StopTimeout < 0 {
			return fmt.Errorf("additionalApps[%q]: stopTimeout must be >= 0", name)
		}
	}
	return nil
}

// UnmarshalYAML reads the entry as the DTO its `type` names. It is one node
// decoded one way — not a merge of two shapes — because a typed entry and a raw
// container disagree about who owns the volume, the data layout and the image,
// and a value that silently means something different depending on a sibling
// key is how a stand ends up with its data in a volume nobody looks at.
func (a *AdditionalAppProps) UnmarshalYAML(value *yaml.Node) error {
	var head struct {
		Type string `yaml:"type"`
	}
	if err := value.Decode(&head); err != nil {
		return fmt.Errorf("read additionalApps entry type: %w", err)
	}
	switch strings.ToUpper(strings.TrimSpace(head.Type)) {
	case AppTypePostgres:
		var p PostgresAppProps
		if err := value.Decode(&p); err != nil {
			return fmt.Errorf("read additionalApps POSTGRES entry: %w", err)
		}
		p.Type = AppTypePostgres
		*a = AdditionalAppProps{Name: p.Name, Type: AppTypePostgres, Enabled: p.Enabled, Postgres: &p}
		return nil
	case AppTypeQdrant:
		var q QdrantAppProps
		if err := value.Decode(&q); err != nil {
			return fmt.Errorf("read additionalApps QDRANT entry: %w", err)
		}
		q.Type = AppTypeQdrant
		*a = AdditionalAppProps{Name: q.Name, Type: AppTypeQdrant, Enabled: q.Enabled, Qdrant: &q}
		return nil
	default:
		// The historical shape. The alias breaks the recursion into this method.
		type rawAdditionalApp AdditionalAppProps
		var raw rawAdditionalApp
		if err := value.Decode(&raw); err != nil {
			return fmt.Errorf("read additionalApps entry: %w", err)
		}
		*a = AdditionalAppProps(raw)
		a.Type = AppTypeContainer
		return nil
	}
}

// IsTyped reports whether this entry is a dependency the launcher knows rather
// than a raw container.
func (a AdditionalAppProps) IsTyped() bool { return a.Type != AppTypeContainer }
