package launchermcp

import (
	"errors"
	"math"
	"strings"
	"time"
)

// SQLite and credential rows use positive Unix nanoseconds. Reserve the maximum
// value for an owner-selected unbounded grant; never expose it as a real date.
const oauthNoDeadline int64 = math.MaxInt64

// ErrOAuthApprovalInvalid reports unusable owner-selected metadata before any decision is saved.
var ErrOAuthApprovalInvalid = errors.New("invalid OAuth approval name or expiry")

// OAuthApproval is owner input only, never accepted from an OAuth client.
// Missing options preserve the original 30-day approval contract.
type OAuthApproval struct {
	Name         string     `json:"name"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	NeverExpires bool       `json:"neverExpires"`
}

func (a OAuthApproval) validate(now time.Time) (OAuthApproval, time.Time, error) {
	a.Name = strings.TrimSpace(a.Name)
	if len(a.Name) > 100 || (a.NeverExpires && a.ExpiresAt != nil) {
		return a, time.Time{}, ErrOAuthApprovalInvalid
	}
	deadline := now.Add(oauthGrantLifetime)
	if a.NeverExpires {
		deadline = time.Unix(0, oauthNoDeadline).UTC()
	} else if a.ExpiresAt != nil {
		deadline = *a.ExpiresAt
		// Bound before UnixNano, whose overflow is otherwise silent.
		if !deadline.After(now) || deadline.After(now.Add(maxCredentialLifetime)) {
			return a, time.Time{}, ErrOAuthApprovalInvalid
		}
	}
	return a, deadline, nil
}
