package appfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAttachClassMatchesSource is the integrity gate on the one artifact in this
// repository that is committed as a build OUTPUT: CiteckAttach.class.
//
// The Go build has no javac and CI must not grow a JDK dependency, so the class
// cannot be rebuilt from source during the build — which means nothing but this
// test stands between "someone edited CiteckAttach.java" and a launcher shipping
// a stale class that silently keeps the old behavior. checksums.sha256 records
// the source and class the committed pair was generated from; regenerate both
// with `make jvm-attach-class`.
func TestAttachClassMatchesSource(t *testing.T) {
	checksums, err := attachChecksums()
	require.NoError(t, err)
	want := parseChecksums(t, string(checksums))
	require.Len(t, want, 2, "checksums.sha256 must cover both the source and the class")

	source, err := attachSource()
	require.NoError(t, err)
	class, err := AttachClass()
	require.NoError(t, err)

	assert.Equal(t, want["CiteckAttach.java"], sha256Hex(source),
		"CiteckAttach.java has changed since the class was generated — run `make jvm-attach-class`")
	assert.Equal(t, want["CiteckAttach.class"], sha256Hex(class),
		"CiteckAttach.class does not match checksums.sha256 — run `make jvm-attach-class`")
}

// TestAttachClassIsUsable pins the two properties the launcher relies on when it
// copies the class into a container: it is a real class file, and its bytecode
// version is not newer than the oldest JVM it may be attached to.
func TestAttachClassIsUsable(t *testing.T) {
	class, err := AttachClass()
	require.NoError(t, err)
	require.Greater(t, len(class), 8)

	assert.Equal(t, []byte{0xCA, 0xFE, 0xBA, 0xBE}, class[:4], "not a java class file")

	// Bytes 6-7 are the major version: 61 == Java 17 (`--release 17` in the
	// Makefile). A class compiled by a newer JDK without --release would be
	// rejected by the target JVM with UnsupportedClassVersionError — and the
	// floor cannot go below 61 either, since UnixDomainSocketAddress is Java 16+.
	major := int(class[6])<<8 | int(class[7])
	assert.Equal(t, 61, major, "class must be built with --release 17")
}

func parseChecksums(t *testing.T, content string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for line := range strings.Lines(content) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, name, found := strings.Cut(line, "  ")
		require.True(t, found, "malformed checksum line: %q", line)
		result[name] = sum
	}
	return result
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
