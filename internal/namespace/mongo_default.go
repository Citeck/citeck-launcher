package namespace

import (
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// ApplyNewNamespaceMongoDefault preserves MongoDB for eproc releases that
// cannot disable its client. Call only at creation, after bundle resolution:
// upgrading an existing namespace must never remove its database implicitly.
// Explicit template choices win. Unknown tags conservatively keep MongoDB.
func ApplyNewNamespaceMongoDefault(cfg *Config, def *bundle.Def) {
	if cfg.MongoDB.Enabled != nil || def == nil {
		return
	}
	eproc, exists := def.Applications[appdef.AppEproc]
	if !exists {
		return
	}
	// The shared image-version DTO ignores build suffixes and treats digest
	// references as unknown. The floor is inclusive, including its build tags.
	floor := deps.Version{Major: 2, Minor: 33}
	if version, ok := deps.ParseImageVersion(eproc.Image); ok && !deps.MovesBackwards(floor, version) {
		return
	}
	enabled := true
	cfg.MongoDB.Enabled = &enabled
}
