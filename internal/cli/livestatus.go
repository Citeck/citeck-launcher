package cli

import (
	"sort"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/output"
)

// renderAppTable formats apps into a table string using output.FormatTable + ColorizeStatus.
// Returns the formatted string and counts (running, stopped, total).
func renderAppTable(apps []api.AppDto) (table string, running, stopped, total int) {
	total = len(apps)

	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })

	headers := []string{"APP", "STATUS", "IMAGE"}
	rows := make([][]string, 0, len(apps))

	for _, app := range apps {
		status := app.Status
		if status == "RUNNING" {
			running++
		}
		if status == "STOPPED" {
			stopped++
		}

		rows = append(rows, []string{
			app.Name,
			output.ColorizeStatus(status),
			app.Image,
		})
	}

	table = output.FormatTable(headers, rows)
	return table, running, stopped, total
}
