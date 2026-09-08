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

// TestDemuxExecOutput_SplitsTheTwoStreams pins the seam migrate.Env.Exec is
// defined over: an exec's stdout and stderr must come back SEPARATELY. The
// PostgreSQL plan parses machine-readable output (database and role lists, row
// counts) from stdout only — psql prints notices and warnings on stderr even
// when it exits 0, and a concatenated stream would make one of those part of
// the answer. Each stream keeps its own write order.
func TestDemuxExecOutput_SplitsTheTwoStreams(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(frame(stdcopy.Stdout, "citeck_emodel\n"))
	buf.Write(frame(stdcopy.Stderr, "NOTICE: extension already exists\n"))
	buf.Write(frame(stdcopy.Stdout, "citeck_uiserv\n"))
	buf.Write(frame(stdcopy.Stderr, "WARNING: no privileges were granted\n"))

	stdout, stderr, err := demuxExecOutput(&buf)
	if err != nil {
		t.Fatalf("demuxExecOutput: %v", err)
	}
	if want := "citeck_emodel\nciteck_uiserv\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if want := "NOTICE: extension already exists\nWARNING: no privileges were granted\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// TestDemuxExecOutput_ReportsAnUnframedStream pins the TTY case: a stream
// without frame headers is not demultiplexable, and the caller must learn that
// (it falls back to reading the stream raw) rather than receive empty output.
func TestDemuxExecOutput_ReportsAnUnframedStream(t *testing.T) {
	_, _, err := demuxExecOutput(strings.NewReader("plain tty output, no frame header\n"))
	if err == nil {
		t.Fatal("demuxExecOutput accepted an unframed stream; the TTY fallback would never run")
	}
}
