package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/moby/moby/client"
)

// ContainerLogsBounded caps bytes at the source, including Docker demultiplexing.
// stdcopy grows a whole-frame buffer before Write, so limiting its writer alone
// would still allocate an arbitrarily large single log line/frame.
func (c *Client) ContainerLogsBounded(ctx context.Context, id string, tail, limit int) (text string, truncated bool, failure error) {
	r, err := c.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: fmt.Sprint(tail)})
	if err != nil {
		return "", false, fmt.Errorf("logs bounded: %w", err)
	}
	defer r.Close()
	return readLogsBounded(r, limit)
}
func readLogsBounded(source io.Reader, limit int) (text string, truncated bool, failure error) {
	if limit < 1 {
		return "", false, nil
	}
	// Headers as well as payload are bounded; empty frames cannot evade the cap.
	sourceLimit := &io.LimitedReader{R: source, N: int64(2*limit + 16)}
	r := bufio.NewReader(sourceLimit)
	h, err := r.Peek(8)
	if err != nil && err != io.EOF {
		return "", false, fmt.Errorf("logs bounded: %w", err)
	}
	framed := len(h) == 8 && h[0] <= 2 && h[1] == 0 && h[2] == 0 && h[3] == 0
	if !framed {
		b, e := io.ReadAll(io.LimitReader(r, int64(limit+1)))
		truncated := len(b) > limit
		if truncated {
			b = b[:limit]
		}
		return string(b), truncated, e
	}
	out := make([]byte, 0, limit)
	for {
		var header [8]byte
		_, err = io.ReadFull(r, header[:])
		if err == io.EOF {
			return string(out), sourceLimit.N == 0, nil
		}
		if err != nil {
			if sourceLimit.N == 0 {
				return string(out), true, nil
			}
			return string(out), true, err
		}
		if header[0] > 2 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return "", false, fmt.Errorf("invalid Docker log frame")
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		remaining := int64(limit - len(out))
		take := min(size, remaining+1)
		b, e := io.ReadAll(io.LimitReader(r, take))
		if e != nil {
			return "", false, fmt.Errorf("logs bounded: %w", e)
		}
		if int64(len(b)) != take {
			return string(out), true, io.ErrUnexpectedEOF
		}
		if int64(len(b)) > remaining {
			out = append(out, b[:remaining]...)
			return string(out), true, nil
		}
		out = append(out, b...)
	}
}
