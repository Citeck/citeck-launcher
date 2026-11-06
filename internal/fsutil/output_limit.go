package fsutil

import (
	"context"
	"sync/atomic"
)

type outputLimitKey struct{}

// OutputLimit is opt-in for deliberate diagnostics. Native callers retain
// their existing output behavior. Truncated describes bytes, not JVM outcome.
type OutputLimit struct {
	Bytes     int
	Truncated atomic.Bool
}

// WithOutputLimit attaches a per-request source-byte diagnostic budget.
func WithOutputLimit(ctx context.Context, limit int) (context.Context, *OutputLimit) {
	v := &OutputLimit{Bytes: limit}
	return context.WithValue(ctx, outputLimitKey{}, v), v
}

// DiagnosticOutputLimit returns the optional budget; nil preserves native behavior.
func DiagnosticOutputLimit(ctx context.Context) *OutputLimit {
	v, _ := ctx.Value(outputLimitKey{}).(*OutputLimit)
	return v
}
