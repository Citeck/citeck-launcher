# Dependency migration progress

The Dependencies dialog follows the daemon's migration and rollback progress.
Closing the dialog does not cancel either operation. The final `stop-new` or
`stop-target` step stops the temporary upgraded container; namespace status
`STOPPED` alone does not establish that a migration has finished.

The authoritative state is available through `GET /api/v1/namespace` and
`GET /api/v1/namespace/dependencies`. The latter also exposes the retained
`lastResult` and any pending rollback. Migration reports are stored under the
Launcher logs directory in `reports/deps-upgrade-*.log` (or `deps-rollback-*.log`).

The daemon clears live progress before publishing a terminal event, so an
immediate refetch cannot reopen the final step. The frontend rejects migration
hydration from requests started before a newer lifecycle event. Earlier
namespace requests cannot overwrite a newer fetch, including the migration
state that disables namespace controls. While a
migration is displayed, it also checks namespace state every three seconds,
even when event-stream pings continue arriving. An absent running migration
returns the dialog to its result or dependency list; it does not manufacture a
successful result from elapsed time or transport health.

Desktop delivery preserves the daemon event order by dispatching each frame
to the windows synchronously. Wails alpha.98 `Event.Emit` schedules a separate
window-dispatch goroutine for each event, so sequential emission alone does
not preserve order. Sequence gaps trigger a quiet, coalesced state refresh;
they do not prove that a connection was lost or restored. The replay cursor
never moves backwards when a late frame arrives.
