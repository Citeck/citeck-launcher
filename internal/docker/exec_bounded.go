package docker

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrExecObservationIncomplete means the command may still be running. Closing
// its output stream is not cancellation of the command and must not invite replay.
var ErrExecObservationIncomplete = errors.New("exec observation incomplete")

// readExecBounded decodes known non-TTY exec frames, retaining separate streams
// under one payload quota. Headers also consume a finite source quota. Neither
// an advertised frame size nor a run of empty frames can grow memory unboundedly.
func readExecBounded(source io.Reader, limit int) (stdout, stderr string, truncated bool, failure error) {
	var out, diagnostic bytes.Buffer
	sourceLimit := &io.LimitedReader{R: source, N: 2*int64(max(limit, 0)) + 16}
	remaining := int64(max(limit, 0))
	for {
		var header [8]byte
		_, err := io.ReadFull(sourceLimit, header[:])
		if sourceLimit.N == 0 {
			return out.String(), diagnostic.String(), true, nil
		}
		if errors.Is(err, io.EOF) {
			return out.String(), diagnostic.String(), false, nil
		}
		if err != nil {
			return out.String(), diagnostic.String(), false, fmt.Errorf("exec frame header: %w", err)
		}
		if (header[0] != 1 && header[0] != 2) || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return out.String(), diagnostic.String(), false, errors.New("invalid Docker exec frame")
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		take := min(size, remaining)
		target := &out
		if header[0] == 2 {
			target = &diagnostic
		}
		n, err := io.CopyN(target, sourceLimit, take)
		remaining -= n
		if sourceLimit.N == 0 {
			return out.String(), diagnostic.String(), true, nil
		}
		if err != nil {
			return out.String(), diagnostic.String(), false, fmt.Errorf("exec frame payload: %w", err)
		}
		if size > take {
			return out.String(), diagnostic.String(), true, nil
		}
	}
}
