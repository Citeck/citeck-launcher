package namespace

import (
	"fmt"
	"maps"
	"slices"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// A namespace runs the stand's own PostgreSQL and any number of others a
// workspace declares — the observer's, today. This file generates every OTHER
// cluster.
//
// WHETHER a cluster exists is decided by the BUNDLE naming its image — the same
// rule as qdrant, and the reason there is no switch to keep in sync: a release
// that does not ship the service does not ship its database either. HOW it is
// configured comes from its `additionalApps:` entry with `type: POSTGRES`. The
// launcher knows no such cluster by itself.
//
// The stand's own `postgres` is deliberately NOT expressible here: it carries a
// volume name every launcher ever shipped agrees on, an init script per webapp
// datasource and a pin seeded from real field data. generateInfra owns it.

// dbDefaultMemoryLimit is what a side database gets when nothing says otherwise.
const dbDefaultMemoryLimit = "512m"

// DatabaseSpec is one cluster's whole configuration after the launcher's defaults and
// the workspace's declaration are merged. Everything the generator, the
// dependency registry and the migration plan need about it is here.
type DatabaseSpec struct {
	ID         string
	VolumeBase string
	Image      string // launcher/workspace fallback; a bundle entry outranks it
	User       string
	// Password is the declaration's `password:` — usually a `${secret:<id>}`
	// reference, resolved when the container is generated — and defaults to
	// the user's name, which is what these internal, unpublished clusters have
	// always used.
	Password    string
	DB          string
	Port        int
	MemoryLimit string
	// Settings are `-c key=value` server settings, applied in key order so the
	// generated command — and with it the deployment hash — cannot depend on
	// map iteration.
	Settings map[string]string
}

// DatabaseSpecs answers which clusters this configuration declares: one per
// `additionalApps:` entry of type POSTGRES, with the image's own defaults
// applied to what the entry leaves out. Exported because the daemon registers
// them as dependencies before it generates anything.
//
// Declaring a cluster is not the same as running it — an entry here still only
// produces a container where the bundle names its image (generateDatabases).
func DatabaseSpecs(wsCfg *bundle.WorkspaceConfig) []DatabaseSpec {
	if wsCfg == nil {
		return nil
	}
	var out []DatabaseSpec
	for _, entry := range wsCfg.AdditionalApps {
		if entry.Type != bundle.AppTypePostgres || entry.Postgres == nil || !entry.IsEnabled() {
			continue
		}
		decl := *entry.Postgres
		if decl.Name == "" || slices.ContainsFunc(out, func(s DatabaseSpec) bool { return s.ID == decl.Name }) {
			continue // validation refuses a duplicate name; the first one wins here
		}
		out = append(out, dbSpecFromDecl(decl).withDefaults())
	}
	return out
}

func dbSpecFromDecl(d bundle.PostgresAppProps) DatabaseSpec {
	return DatabaseSpec{
		ID: d.Name, VolumeBase: d.VolumeBase, Image: string(d.Image),
		User: d.User, Password: d.Password, DB: d.DB, Port: d.Port,
		MemoryLimit: d.MemoryLimit, Settings: d.Settings,
	}
}

// withDefaults applies the image's own rules to what the declaration left out,
// so the smallest usable entry in workspace-v1.yml is a bare id.
func (s DatabaseSpec) withDefaults() DatabaseSpec {
	if s.User == "" {
		s.User = "postgres"
	}
	if s.DB == "" {
		s.DB = s.User // what the image does with POSTGRES_DB unset
	}
	if s.Password == "" {
		s.Password = s.User
	}
	if s.VolumeBase == "" {
		s.VolumeBase = s.ID
	}
	if s.MemoryLimit == "" {
		s.MemoryLimit = dbDefaultMemoryLimit
	}
	return s
}

// Descriptor is how this cluster is registered in the dependency registry: the
// PostgreSQL rules, keyed to its own id, container and volume stem.
func (s DatabaseSpec) Descriptor() deps.Descriptor {
	return deps.NewPostgresDescriptor(deps.ID(s.ID), s.ID, s.VolumeBase)
}

// generateDatabases emits every known cluster the bundle names an image for.
//
// The image is the whole condition, exactly as it is for qdrant and for the
// observer: a stand has the service its release ships, and nothing else. It is
// also read through resolveAppImage rather than from ctx.Applications, so the
// answer is a pure function of the configuration and WillGenerateDatabases can
// restate it for the daemon's pin seeding before Generate has run.
//
// Each cluster then goes through the dependency gate exactly as the stand's own
// database does: the image is the gate's answer (a breaking bundle version is
// held back and reported), the volume comes from the generation counter, and
// the data LAYOUT follows the major that will run — an explicit PGDATA up to
// 17, the image's parent-mount default from 18.
func generateDatabases(ctx *NsGenContext) {
	for _, s := range DatabaseSpecs(ctx.WorkspaceConfig) {
		if !databaseImageNamed(ctx, s) {
			continue
		}
		generateDatabase(ctx, s)
	}
}

// databaseImageNamed answers whether anything names an image for this cluster:
// the bundle's `dependencies:` section in practice, a workspace entry's own
// `image:` as a fallback for a stand that pins it there.
func databaseImageNamed(ctx *NsGenContext, s DatabaseSpec) bool {
	return resolveAppImage(ctx, s.ID, "", s.Image) != ""
}

func generateDatabase(ctx *NsGenContext, s DatabaseSpec) {
	id := deps.ID(s.ID)
	chain := resolveAppImageChain(ctx, s.ID, "", s.Image)
	image := resolveDependencyImage(ctx, id, chain)
	major := 17
	if v, ok := deps.ParseImageVersion(image); ok {
		major = v.Major
	}
	layout := deps.PostgresLayoutFor(major)

	app := ctx.GetOrCreateApp(s.ID)
	app.Image = image
	app.Kind = appdef.KindThirdParty
	app.AddEnv("POSTGRES_DB", s.DB)
	app.AddEnv("POSTGRES_USER", s.User)
	app.AddEnv("POSTGRES_PASSWORD", resolveTemplateVarsWithContext(s.Password, ctx))
	if layout.PGData != "" {
		app.AddEnv("PGDATA", layout.PGData)
	}
	if s.Port > 0 {
		// Desktop only in practice: server mode drops every non-proxy publish
		// (see Generate). It is what lets the owning service be run outside the
		// launcher against this database.
		app.AddPort(fmt.Sprintf("%d:%d", s.Port, PGPort))
	}
	app.AddVolume(resolveDependencyVolume(ctx, id) + ":" + layout.MountPath)
	if len(s.Settings) > 0 {
		cmd := make([]string, 0, len(s.Settings)*2)
		for _, k := range slices.Sorted(maps.Keys(s.Settings)) {
			cmd = append(cmd, "-c", k+"="+s.Settings[k])
		}
		app.Cmd = cmd
	}
	app.StartupConditions = []appdef.StartupCondition{
		{Log: &appdef.LogStartupCondition{Pattern: ".*database system is ready to accept connections.*"}},
		{Probe: &appdef.AppProbeDef{
			Exec: &appdef.ExecProbeDef{
				Command: []string{"/bin/sh", "-c", fmt.Sprintf("pg_isready -U %s || exit 1", s.User)},
			},
			PeriodSeconds:    10,
			FailureThreshold: 60,
			TimeoutSeconds:   5,
		}},
	}
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: s.MemoryLimit}}
	app.LivenessProbe = &appdef.AppProbeDef{
		Exec:             &appdef.ExecProbeDef{Command: []string{"pg_isready", "-U", s.User}},
		FailureThreshold: livenessFailureThreshold,
		TimeoutSeconds:   5,
	}
}

// WillGenerateDatabases answers, WITHOUT generating, which known clusters a
// namespace with this configuration emits — the set the daemon must seed pins
// for. It is the same pure condition generateDatabases applies.
func WillGenerateDatabases(cfg *Config, bun *bundle.Def, wsCfg *bundle.WorkspaceConfig) map[string]bool {
	specs := DatabaseSpecs(wsCfg)
	out := make(map[string]bool, len(specs))
	// Answered with a false rather than left out, for the same reason the
	// qdrant restatement is: the caller defaults every registered dependency to
	// present, and a cluster missing from this map would be probed on every
	// load of every stand whose release does not ship it.
	for _, s := range specs {
		out[s.ID] = false
	}
	if cfg == nil || bun == nil {
		return out
	}
	ctx := NewNsGenContext(cfg, bun)
	ctx.WorkspaceConfig = wsCfg
	for _, s := range specs {
		out[s.ID] = databaseImageNamed(ctx, s)
	}
	return out
}
