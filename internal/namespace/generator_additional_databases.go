package namespace

import (
	"log/slog"
	"slices"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
)

// wireAdditionalAppDatabases creates, in the stand's own postgres, the
// databases additionalApps entries ask for (postgresDatabases) — with the same
// init action the platform uses for its webapps' datasources, so the database
// and its role follow the platform's convention and are dumped, migrated and
// rolled back with the rest of the cluster.
//
// ALWAYS, for every entry of the workspace that lists them, whether or not the
// service is generated on this stand — the rule Keycloak's database follows
// (generateKeycloak). The init actions feed the postgres deployment hash, so a
// database that came and went with its service would recreate postgres every
// time a feature is switched on or off; an empty database costs nothing.
func wireAdditionalAppDatabases(ctx *NsGenContext) {
	if ctx.WorkspaceConfig == nil {
		return
	}
	pg := ctx.Applications[appdef.AppPostgres]
	if pg == nil {
		return
	}
	var dbs []string
	for _, def := range ctx.WorkspaceConfig.AdditionalApps {
		if def.IsTyped() || len(def.PostgresDatabases) == 0 {
			continue
		}
		name := strings.TrimSpace(def.Name)
		for _, db := range def.PostgresDatabases {
			// Validation drops such an entry when the workspace config loads;
			// this guard keeps a config that skipped it from reaching `sh -c`.
			if err := bundle.ValidatePostgresDatabaseName(db); err != nil {
				slog.Error("additionalApps entry asks for an invalid postgres database; not created",
					"name", name, "err", err)
				continue
			}
			dbs = append(dbs, db)
		}
	}
	slices.Sort(dbs)
	for _, db := range slices.Compact(dbs) {
		action := appdef.AppInitAction{Exec: []string{"sh", "-c", "/init_db_and_user.sh " + db}}
		if slices.ContainsFunc(pg.InitActions, func(a appdef.AppInitAction) bool {
			return slices.Equal(a.Exec, action.Exec)
		}) {
			continue
		}
		pg.InitActions = append(pg.InitActions, action)
	}
}
