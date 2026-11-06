package jvmattach

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type partialRequestWriter struct{ err error }

func (w partialRequestWriter) Write(_ []byte) (int, error) { return 2, w.err }

func TestWriteRequestMarksPartialDeliveryUncertain(t *testing.T) {
	for _, cause := range []error{nil, io.ErrClosedPipe} {
		err := writeRequest(partialRequestWriter{cause}, "1\x00jcmd\x00GC.run\x00\x00\x00")
		var dispatched *DispatchError
		require.ErrorAs(t, err, &dispatched)
		require.True(t, dispatched.Uncertain)
		if cause != nil {
			require.ErrorIs(t, err, cause)
		} else {
			require.ErrorIs(t, err, io.ErrShortWrite)
		}
	}
}

func TestParseResponseDistinguishesLostReplyFromJVMFailure(t *testing.T) {
	for _, raw := range []string{"0", "not-a-code\n", "1\nUnknown command"} {
		_, err := parseResponse(raw)
		var dispatched *DispatchError
		require.ErrorAs(t, err, &dispatched)
		require.Equal(t, raw != "1\nUnknown command", dispatched.Uncertain)
		require.ErrorIs(t, err, dispatched.Err)
	}
}
