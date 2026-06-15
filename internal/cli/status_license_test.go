package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/i18n"
)

// TestFormatLicenseLine pins the four states of the `citeck status` license
// line: omitted (older daemon), enterprise, enterprise-expiring-soon, expired,
// and community. Colors are disabled in tests (non-TTY), so the assertions
// see plain text.
func TestFormatLicenseLine(t *testing.T) {
	i18n.InitI18n("en")
	t.Cleanup(i18n.ResetForTest)

	t.Run("nil status omits the line", func(t *testing.T) {
		assert.Empty(t, formatLicenseLine(nil))
	})

	t.Run("enterprise", func(t *testing.T) {
		got := formatLicenseLine(&api.LicenseStatusDto{
			Enterprise: true, Tenant: "acme", ValidUntil: "2026-09-01", DaysLeft: 83,
		})
		assert.Equal(t, "enterprise (acme), valid until 2026-09-01", got)
	})

	t.Run("enterprise expiring soon", func(t *testing.T) {
		got := formatLicenseLine(&api.LicenseStatusDto{
			Enterprise: true, Tenant: "acme", ValidUntil: "2026-06-20",
			DaysLeft: 10, ExpiringSoon: true,
		})
		assert.Contains(t, got, "enterprise (acme), valid until 2026-06-20")
		assert.Contains(t, got, "10")
	})

	t.Run("expired", func(t *testing.T) {
		got := formatLicenseLine(&api.LicenseStatusDto{
			Enterprise: false, Tenant: "acme", ValidUntil: "2025-01-01", DaysLeft: -100,
		})
		assert.Equal(t, "enterprise (acme), expired on 2025-01-01", got)
	})

	t.Run("community", func(t *testing.T) {
		got := formatLicenseLine(&api.LicenseStatusDto{})
		assert.Equal(t, "community", got)
	})
}
