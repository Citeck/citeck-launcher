package h2migrate

import (
	"io"
)

// readVarInt reads an H2 MVStore variable-length integer (LEB128 / protobuf-style).
// Each byte uses 7 data bits + 1 continuation bit (high bit).
// If the high bit is set, more bytes follow.
func readVarInt(data []byte, pos int) (result int64, consumed int, _ error) {
	if pos >= len(data) {
		return 0, 0, io.ErrUnexpectedEOF
	}

	var shift uint

	for {
		if pos+consumed >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		b := data[pos+consumed]
		consumed++

		result |= int64(b&0x7F) << shift
		if b&0x80 == 0 {
			return result, consumed, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, 0, io.ErrUnexpectedEOF
		}
	}
}

// readVarString reads an H2 MVStore string: varint(charCount) + modified-UTF8 bytes.
// For ASCII strings (all meta map keys), charCount == byteCount.
func readVarString(data []byte, pos int) (s string, n int, _ error) {
	charCount, n, err := readVarInt(data, pos)
	if err != nil {
		return "", 0, err
	}
	pos += n
	total := n

	// Read charCount characters in modified UTF-8
	buf := make([]byte, 0, int(charCount))
	for range charCount {
		if pos >= len(data) {
			return "", 0, io.ErrUnexpectedEOF
		}
		b := data[pos]
		charLen := modifiedUTF8CharLen(b)
		if pos+charLen > len(data) {
			return "", 0, io.ErrUnexpectedEOF
		}
		buf = append(buf, data[pos:pos+charLen]...)
		pos += charLen
		total += charLen
	}

	return string(buf), total, nil
}

// modifiedUTF8CharLen returns the byte length of a modified UTF-8 character.
func modifiedUTF8CharLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	default:
		return 1 // single byte fallback
	}
}
