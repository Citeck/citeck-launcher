package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/output"
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

// statusExtrasSpy counts the round-trips `citeck status` makes on top of the
// namespace DTO and answers whatever the test set.
type statusExtrasSpy struct {
	lic      *api.LicenseStatusDto
	deps     *api.DependenciesDto
	licErr   error
	depsErr  error
	licCalls int
	depCalls int
}

func (s *statusExtrasSpy) GetLicenseStatus() (*api.LicenseStatusDto, error) {
	s.licCalls++
	return s.lic, s.licErr
}

func (s *statusExtrasSpy) GetDependencies() (*api.DependenciesDto, error) {
	s.depCalls++
	return s.deps, s.depsErr
}

// The license line and the dependency hint exist only in the TEXT rendering:
// PrintResult marshals the namespace DTO alone, so in --format json those two
// requests buy output that is never produced — two extra round-trips against a
// daemon a scripted caller is usually polling in a loop.
func TestFetchStatusExtras_JSONModeFetchesNothing(t *testing.T) {
	prev := output.GetFormat()
	output.SetFormat(output.FormatJSON)
	t.Cleanup(func() { output.SetFormat(prev) })

	spy := &statusExtrasSpy{lic: &api.LicenseStatusDto{Enterprise: true}, deps: &api.DependenciesDto{}}
	lic, deps := fetchStatusExtras(spy)
	assert.Nil(t, lic)
	assert.Nil(t, deps)
	assert.Zero(t, spy.licCalls, "the license status is not in the JSON output")
	assert.Zero(t, spy.depCalls, "the dependency state is not in the JSON output")
}

func TestFetchStatusExtras_TextModeFetchesBoth(t *testing.T) {
	prev := output.GetFormat()
	output.SetFormat(output.FormatText)
	t.Cleanup(func() { output.SetFormat(prev) })

	spy := &statusExtrasSpy{lic: &api.LicenseStatusDto{Enterprise: true}, deps: &api.DependenciesDto{RollbackPending: "x"}}
	lic, deps := fetchStatusExtras(spy)
	assert.Same(t, spy.lic, lic)
	assert.Same(t, spy.deps, deps)
	assert.Equal(t, 1, spy.licCalls)
	assert.Equal(t, 1, spy.depCalls)
}

// Both are best-effort: an older daemon has no endpoint, a locked secret store
// errors, and a namespace that is not configured yet answers 400. Each failure
// drops its own line — it never fails `citeck status`.
func TestFetchStatusExtras_ErrorsDropTheLineNotTheCommand(t *testing.T) {
	prev := output.GetFormat()
	output.SetFormat(output.FormatText)
	t.Cleanup(func() { output.SetFormat(prev) })

	spy := &statusExtrasSpy{
		lic: &api.LicenseStatusDto{}, licErr: errors.New("secrets locked"),
		deps: &api.DependenciesDto{}, depsErr: errors.New("no namespace"),
	}
	lic, deps := fetchStatusExtras(spy)
	assert.Nil(t, lic, "a partial license answer beside an error must not be rendered")
	assert.Nil(t, deps)
}
