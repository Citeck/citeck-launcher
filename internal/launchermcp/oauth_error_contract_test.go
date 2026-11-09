package launchermcp

import (
	"errors"
	"testing"

	"github.com/ory/fosite"
	"github.com/stretchr/testify/require"
)

func TestWrappedOAuthProtocolErrorsKeepFositeIdentity(t *testing.T) {
	for _, protocol := range []*fosite.RFC6749Error{
		fosite.ErrInvalidRequest.WithDescription("registered OAuth redirect required"),
		fosite.ErrInvalidGrant.WithDescription("OAuth code redirect mismatch"),
	} {
		wrapped := wrapOAuthProtocolError(protocol)
		require.ErrorIs(t, wrapped, protocol)
		actual, ok := errors.AsType[*fosite.RFC6749Error](wrapped)
		require.True(t, ok)
		require.Same(t, protocol, actual)
		require.Equal(t, protocol.DescriptionField, actual.DescriptionField)
		// Fosite's own mapper must still recognize the wrapped pointer.
		require.Same(t, protocol, fosite.ErrorToRFC6749Error(wrapped))
	}
}
