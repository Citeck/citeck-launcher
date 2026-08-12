package docker

import (
	"archive/tar"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Docker copy API takes a tar stream, so the tar HEADER — not the call —
// decides what lands in the container. Two properties of that header are
// contracts: the file must be world-readable (containers run as their image's
// user: postgres 999, keycloak 1000, webapps root) and it must NOT be
// executable — the launcher's only user of this is the JVM attach class, and
// the launcher does not put executables into a running production container.
func TestTarSingleFile_ModeIsReadableAndNotExecutable(t *testing.T) {
	payload := []byte{0xCA, 0xFE, 0xBA, 0xBE, 0x00, 0x01}

	buf, err := tarSingleFile("CiteckAttach.class", payload)
	require.NoError(t, err)

	tr := tar.NewReader(buf)
	hdr, err := tr.Next()
	require.NoError(t, err)

	assert.Equal(t, "CiteckAttach.class", hdr.Name)
	assert.Equal(t, byte(tar.TypeReg), hdr.Typeflag)
	assert.Equal(t, int64(0o644), hdr.Mode)
	assert.Zero(t, hdr.Mode&0o111, "a copied file must never be executable")
	assert.Equal(t, int64(len(payload)), hdr.Size)
	// uid/gid 0: CopyUIDGID is off, so the header owns the ownership.
	assert.Equal(t, 0, hdr.Uid)
	assert.Equal(t, 0, hdr.Gid)

	body, err := io.ReadAll(tr)
	require.NoError(t, err)
	assert.Equal(t, payload, body)

	_, err = tr.Next()
	assert.ErrorIs(t, err, io.EOF, "exactly one entry")
}
