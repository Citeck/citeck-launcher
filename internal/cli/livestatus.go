package cli

import (
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
	"github.com/citeck/citeck-launcher/internal/output"
)

// StreamReloadStatus waits for all services to reach terminal state after a reload/config change.
// Shows live table in TTY mode. Returns nil on success, errInterrupted on Ctrl+C.
// Exported so cli/setup can call it.
func StreamReloadStatus(c *client.DaemonClient) error {
	ensureI18n()
	_, err := streamLiveStatus(c, liveStatusOpts{
		initialDelay: 1 * time.Second,
		successMsg:   output.Colorize(output.Green, t("reload.complete")),
	})
	return err
}

// renderAppTable is a convenience wrapper around output.FormatAppTable
// that returns the same 4-tuple used by streamLiveStatus.
func renderAppTable(apps []api.AppDto) (table string, running, failed, total int) {
	r := output.FormatAppTable(apps)
	return r.Table, r.Running, r.Failed, r.Total
}
