package cli

import (
	"sort"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/output"
)

// renderAppTable formats apps into a table string using output.FormatTable + ColorizeStatus.
// Returns the formatted string and counts (running, failed, total).
func renderAppTable(apps []api.AppDto) (table string, running, failed, total int) {
	total = len(apps)

	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })

	headers := []string{"APP", "STATUS", "IMAGE"}
	rows := make([][]string, 0, len(apps))

	for _, app := range apps {
		switch app.Status {
		case "RUNNING":
			running++
		case "START_FAILED", "PULL_FAILED", "FAILED":
			failed++
		}

		rows = append(rows, []string{
			app.Name,
			output.ColorizeStatus(app.Status),
			app.Image,
		})
	}

	table = output.FormatTable(headers, rows)
	return table, running, failed, total
}
