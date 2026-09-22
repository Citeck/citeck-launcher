package namespace

import (
	"fmt"
	"maps"
	"slices"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// A namespace runs the stand's own PostgreSQL and, since the observer arrived,
// at least one more. This file generates every OTHER cluster from a
// DECLARATION rather than from Go code, so that adding a database is a
// workspace-config change: `databases:` in workspace-v1.yml (bundle.DatabaseProps).
//
// The launcher still ships built-in defaults for the cluster it knows — the
// observer's — so nothing depends on a config change landing first; a workspace
// entry with the same id overrides those defaults field by field.
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
	// Password is NOT configurable: see bundle.DatabaseProps. It defaults to
	// the user's name, which is what these internal, unpublished clusters have
	// always used.
	Password    string
	DB          string
	Port        int
	MemoryLimit string
	// RequiredBy is the app this database exists for; "" means unconditional.
	RequiredBy string
	// Settings are `-c key=value` server settings, applied in key order so the
	// generated command — and with it the deployment hash — cannot depend on
	// map iteration.
	Settings map[string]string
}

// builtinDatabases are the clusters this launcher knows without being told.
//
// The observer's is here rather than in generateObserver so that it is the SAME
// code path a workspace-declared database takes: one generator, one set of
// rules, and a change to either applies to both.
func builtinDatabases() []DatabaseSpec {
	return []DatabaseSpec{{
		ID:         appdef.AppObsPostgres,
		VolumeBase: "obs_postgres",
		// The launcher's own default matches generatePostgres's, pinned to the
		// patch: a bundle's `dependencies:` entry is how the version moves.
		Image:      "postgres:17.5",
		User:       "observer",
		Password:   "observer",
		DB:         "observer",
		Port:       14524,
		RequiredBy: appdef.AppObserver,
		// Tuned for the observability workload: heavy writes (span/metric
		// ingestion), aggregating queries, JSONB GIN lookups.
		Settings: map[string]string{
			"shared_buffers":               "256MB",
			"work_mem":                     "32MB",
			"maintenance_work_mem":         "128MB",
			"effective_cache_size":         "1GB",
			"random_page_cost":             "1.1",
			"checkpoint_completion_target": "0.9",
			"wal_buffers":                  "16MB",
			"max_wal_size":                 "1GB",
			"min_wal_size":                 "256MB",
		},
	}}
}

// DatabaseSpecs answers which clusters this configuration declares, built-ins
// merged with the workspace's own — exported because the daemon registers them
// as dependencies before it generates anything.
//
// The merge is FIELD BY FIELD: a workspace that only wants a different memory
// limit writes that one key and keeps every other default. A workspace entry
// with an unknown id simply adds a cluster.
func DatabaseSpecs(wsCfg *bundle.WorkspaceConfig) []DatabaseSpec {
	out := builtinDatabases()
	if wsCfg == nil {
		return out
	}
	for _, decl := range wsCfg.Databases {
		if decl.ID == "" {
			continue
		}
		i := slices.IndexFunc(out, func(s DatabaseSpec) bool { return s.ID == decl.ID })
		if i < 0 {
			out = append(out, dbSpecFromDecl(decl))
			continue
		}
		out[i] = mergeDBSpec(out[i], decl)
	}
	for i := range out {
		out[i] = out[i].withDefaults()
	}
	return out
}

func dbSpecFromDecl(d bundle.DatabaseProps) DatabaseSpec {
	return DatabaseSpec{
		ID: d.ID, VolumeBase: d.VolumeBase, Image: string(d.Image),
		User: d.User, DB: d.DB, Port: d.Port,
		MemoryLimit: d.MemoryLimit, RequiredBy: d.RequiredBy, Settings: d.Settings,
	}
}

func mergeDBSpec(base DatabaseSpec, d bundle.DatabaseProps) DatabaseSpec {
	if d.VolumeBase != "" {
		base.VolumeBase = d.VolumeBase
	}
	if d.Image != "" {
		base.Image = string(d.Image)
	}
	if d.User != "" {
		base.User = d.User
	}
	if d.DB != "" {
		base.DB = d.DB
	}
	if d.Port != 0 {
		base.Port = d.Port
	}
	if d.MemoryLimit != "" {
		base.MemoryLimit = d.MemoryLimit
	}
	if d.RequiredBy != "" {
		base.RequiredBy = d.RequiredBy
	}
	if len(d.Settings) > 0 {
		base.Settings = d.Settings
	}
	return base
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

// databaseOwnerPresent answers RequiredBy, and it is deliberately a PURE
// function of the configuration — the same one the daemon's pin seeding calls
// before Generate has run. Asking `ctx.Applications` instead would make the
// generator's answer unpredictable from outside, which is the trap
// WillGenerateQdrant exists to document: a restatement that drifts hands an
// existing cluster to a bundle's image with no pin to hold it back.
func databaseOwnerPresent(ctx *NsGenContext, owner string) bool {
	if owner == "" {
		return true
	}
	if resolveAppImage(ctx, owner, "", "") != "" {
		return true
	}
	if ctx.WorkspaceConfig != nil {
		for _, app := range ctx.WorkspaceConfig.AdditionalApps {
			if app.Name == owner {
				return true
			}
		}
	}
	return false
}

// generateDatabases emits every declared cluster whose owner this namespace has.
//
// Each one goes through the dependency gate exactly as the stand's own database
// does: the image is the gate's answer (a breaking bundle version is held back
// and reported), the volume comes from the generation counter, and the data
// LAYOUT follows the major that will run — an explicit PGDATA up to 17, the
// image's parent-mount default from 18.
func generateDatabases(ctx *NsGenContext) {
	for _, s := range DatabaseSpecs(ctx.WorkspaceConfig) {
		if !databaseOwnerPresent(ctx, s.RequiredBy) {
			continue
		}
		generateDatabase(ctx, s)
	}
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
	app.AddEnv("POSTGRES_PASSWORD", s.Password)
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

// WillGenerateDatabases answers, WITHOUT generating, which declared clusters a
// namespace with this configuration emits — the set the daemon must seed pins
// for. It is the same pure condition generateDatabases applies.
func WillGenerateDatabases(cfg *Config, bun *bundle.Def, wsCfg *bundle.WorkspaceConfig) map[string]bool {
	out := map[string]bool{}
	if cfg == nil || bun == nil {
		return out
	}
	ctx := NewNsGenContext(cfg, bun)
	ctx.WorkspaceConfig = wsCfg
	for _, s := range DatabaseSpecs(wsCfg) {
		out[s.ID] = databaseOwnerPresent(ctx, s.RequiredBy)
	}
	return out
}

// databaseSpecFor answers one cluster's declaration by id, for a generator that
// has to AGREE with it — the observer's, which must tell its service the
// credentials and the port its database was declared with. An id nothing
// declares yields the zero spec with the image's own defaults applied, so a
// caller never has to nil-check.
func databaseSpecFor(ctx *NsGenContext, id string) DatabaseSpec {
	for _, s := range DatabaseSpecs(ctx.WorkspaceConfig) {
		if s.ID == id {
			return s
		}
	}
	return DatabaseSpec{ID: id}.withDefaults()
}
