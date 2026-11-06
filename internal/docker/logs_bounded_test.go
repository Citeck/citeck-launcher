package docker

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type countedLogReader struct {
	r io.Reader
	n int
}

//nolint:wrapcheck // Transparent source instrumentation must preserve exact EOF for demultiplexing.
func (c *countedLogReader) Read(b []byte) (int, error) { n, e := c.r.Read(b); c.n += n; return n, e }
func TestReadLogsBoundedCapsSourceAndHugeFrames(t *testing.T) {
	for _, framed := range []bool{false, true} {
		var input bytes.Buffer
		if framed {
			header := make([]byte, 8)
			header[0] = 1
			binary.BigEndian.PutUint32(header[4:], 1<<30)
			input.Write(header)
		}
		input.WriteString(strings.Repeat("x", 1<<20))
		r := &countedLogReader{r: &input}
		out, truncated, e := readLogsBounded(r, 1024)
		require.NoError(t, e)
		require.Len(t, out, 1024)
		require.True(t, truncated)
		require.LessOrEqual(t, r.n, 2064, "must not read a whole source/frame before bounding")
	}
	var input bytes.Buffer
	for range 1000 {
		input.Write(make([]byte, 8))
	}
	out, truncated, e := readLogsBounded(&input, 128)
	require.NoError(t, e)
	require.Empty(t, out)
	require.True(t, truncated, "empty frames must not evade source bound")
}
