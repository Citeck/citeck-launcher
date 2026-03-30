package h2migrate

import (
	"io"
)

// readVarInt reads an H2 MVStore variable-length integer (LEB128 / protobuf-style).
// Each byte uses 7 data bits + 1 continuation bit (high bit).
// If the high bit is set, more bytes follow.
func readVarInt(data []byte, pos int) (int64, int, error) {
	if pos >= len(data) {
		return 0, 0, io.ErrUnexpectedEOF
	}

	var result int64
	var shift uint
	consumed := 0

	for {
		if pos+consumed >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		b := data[pos+consumed]
		consumed++

		result |= int64(b&0x7F) << shift
		if b&0x80 == 0 {
			// High bit clear — last byte
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
func readVarString(data []byte, pos int) (string, int, error) {
	charCount, n, err := readVarInt(data, pos)
	if err != nil {
		return "", 0, err
	}
	pos += n
	total := n

	// Read charCount characters in modified UTF-8
	buf := make([]byte, 0, int(charCount))
	for i := int64(0); i < charCount; i++ {
		if pos >= len(data) {
			return "", 0, io.ErrUnexpectedEOF
		}
		b := data[pos]
		if b < 0x80 {
			// ASCII: 1 byte
			buf = append(buf, b)
			pos++
			total++
		} else if b&0xE0 == 0xC0 {
			// 2-byte: 110xxxxx 10xxxxxx
			if pos+1 >= len(data) {
				return "", 0, io.ErrUnexpectedEOF
			}
			buf = append(buf, b, data[pos+1])
			pos += 2
			total += 2
		} else if b&0xF0 == 0xE0 {
			// 3-byte: 1110xxxx 10xxxxxx 10xxxxxx
			if pos+2 >= len(data) {
				return "", 0, io.ErrUnexpectedEOF
			}
			buf = append(buf, b, data[pos+1], data[pos+2])
			pos += 3
			total += 3
		} else {
			// Treat as single byte fallback
			buf = append(buf, b)
			pos++
			total++
		}
	}

	return string(buf), total, nil
}
