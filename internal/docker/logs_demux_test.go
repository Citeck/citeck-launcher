package docker

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
)

// frame builds one Docker multiplexed log frame: an 8-byte header
// (stream-type byte, three zero bytes, big-endian uint32 payload length)
// followed by the payload. Mirrors the wire format stdcopy.StdCopy demuxes.
func frame(t stdcopy.StdType, payload string) []byte {
	out := make([]byte, 8, 8+len(payload))
	out[0] = byte(t)
	binary.BigEndian.PutUint32(out[4:], uint32(len(payload)))
	return append(out, []byte(payload)...)
}

// TestDemuxContainerLogs_PreservesInterleavedOrder pins the fix for the
// "the end of the log differs per tail size" bug: Docker's Tail=N applies to
// the CHRONOLOGICALLY interleaved stdout+stderr stream, but the old backlog
// demux copied stdout and stderr into SEPARATE buffers and concatenated them
// (all stdout, then all stderr). That reordered every stderr line to the very
// bottom regardless of when it was written — a Java app's startup stderr
// WARNINGs landed AFTER a runtime stdout INFO line hours newer, and which
// stderr lines got dumped there changed with the tail window. The live-follow
// path already writes both streams to one sink and preserves order; the
// backlog path must do the same.
func TestDemuxContainerLogs_PreservesInterleavedOrder(t *testing.T) {
	var buf bytes.Buffer
	// Chronological order on the wire: out, err, out, err, out.
	buf.Write(frame(stdcopy.Stdout, "INFO app started\n"))
	buf.Write(frame(stdcopy.Stderr, "WARN native access\n"))
	buf.Write(frame(stdcopy.Stdout, "INFO handling request\n"))
	buf.Write(frame(stdcopy.Stderr, "WARN deprecated method\n"))
	buf.Write(frame(stdcopy.Stdout, "INFO ZK licenses updated\n"))

	got, err := demuxContainerLogs(&buf)
	if err != nil {
		t.Fatalf("demuxContainerLogs: %v", err)
	}

	want := "INFO app started\n" +
		"WARN native access\n" +
		"INFO handling request\n" +
		"WARN deprecated method\n" +
		"INFO ZK licenses updated\n"
	if got != want {
		t.Errorf("interleaving not preserved:\n got  %q\n want %q", got, want)
	}

	// Guard against the specific regression: the newest line (a stdout INFO)
	// must be LAST, not buried above a stderr block shoved to the bottom.
	if !strings.HasSuffix(got, "INFO ZK licenses updated\n") {
		t.Errorf("newest stdout line is not last; stderr was reordered to the bottom: %q", got)
	}
}
