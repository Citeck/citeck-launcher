package h2migrate

import (
	"fmt"
	"io"
)

// readVarInt reads a variable-length integer from a byte slice.
// H2 MVStore uses a custom varint encoding:
//   - If the first byte has bit 7 clear (0xxxxxxx): value is the byte itself (0..127)
//   - If bits 7-6 are 10 (10xxxxxx): 2-byte value, 14 bits
//   - If bits 7-5 are 110 (110xxxxx): 3-byte value, 21 bits
//   - If bits 7-4 are 1110 (1110xxxx): 4-byte value, 28 bits
//   - If bits 7-3 are 11110 (11110xxx): 5-byte value, 35 bits
//   - If bits 7-2 are 111110 (111110xx): 6-byte value, 42 bits
//   - If first byte is 0xFC (11111100): next 4 bytes as big-endian int32
//   - If first byte is 0xFD (11111101): next 8 bytes as big-endian int64
//   - If first byte is 0xFE (11111110): next 4 bytes as big-endian int32 (unsigned treated as long)
//   - If first byte is 0xFF (11111111): next 8 bytes as big-endian int64
//
// Returns the decoded value and the number of bytes consumed.
func readVarInt(data []byte, pos int) (int64, int, error) {
	if pos >= len(data) {
		return 0, 0, io.ErrUnexpectedEOF
	}

	b := data[pos]

	if b <= 127 { // bit 7 is clear: 0xxxxxxx
		return int64(b), 1, nil
	}

	switch {
	case b&0xC0 == 0x80: // 10xxxxxx: 2 bytes, 14 bits
		if pos+1 >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		val := int64(b&0x3F)<<8 | int64(data[pos+1])
		return val, 2, nil

	case b&0xE0 == 0xC0: // 110xxxxx: 3 bytes, 21 bits
		if pos+2 >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		val := int64(b&0x1F)<<16 | int64(data[pos+1])<<8 | int64(data[pos+2])
		return val, 3, nil

	case b&0xF0 == 0xE0: // 1110xxxx: 4 bytes, 28 bits
		if pos+3 >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		val := int64(b&0x0F)<<24 | int64(data[pos+1])<<16 | int64(data[pos+2])<<8 | int64(data[pos+3])
		return val, 4, nil

	case b&0xF8 == 0xF0: // 11110xxx: 5 bytes, 35 bits
		if pos+4 >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		val := int64(b&0x07)<<32 | int64(data[pos+1])<<24 | int64(data[pos+2])<<16 |
			int64(data[pos+3])<<8 | int64(data[pos+4])
		return val, 5, nil

	case b&0xFC == 0xF8: // 111110xx: 6 bytes, 42 bits
		if pos+5 >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		val := int64(b&0x03)<<40 | int64(data[pos+1])<<32 | int64(data[pos+2])<<24 |
			int64(data[pos+3])<<16 | int64(data[pos+4])<<8 | int64(data[pos+5])
		return val, 6, nil

	case b == 0xFC: // next 4 bytes as big-endian int32
		if pos+4 >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		val := int64(int32(uint32(data[pos+1])<<24 | uint32(data[pos+2])<<16 | uint32(data[pos+3])<<8 | uint32(data[pos+4])))
		return val, 5, nil

	case b == 0xFD: // next 8 bytes as big-endian int64
		if pos+8 >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		val := int64(data[pos+1])<<56 | int64(data[pos+2])<<48 | int64(data[pos+3])<<40 |
			int64(data[pos+4])<<32 | int64(data[pos+5])<<24 | int64(data[pos+6])<<16 |
			int64(data[pos+7])<<8 | int64(data[pos+8])
		return val, 9, nil

	case b == 0xFE: // next 4 bytes as unsigned int32 → long
		if pos+4 >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		val := int64(uint32(data[pos+1])<<24 | uint32(data[pos+2])<<16 | uint32(data[pos+3])<<8 | uint32(data[pos+4]))
		return val, 5, nil

	case b == 0xFF: // next 8 bytes as big-endian int64
		if pos+8 >= len(data) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		val := int64(data[pos+1])<<56 | int64(data[pos+2])<<48 | int64(data[pos+3])<<40 |
			int64(data[pos+4])<<32 | int64(data[pos+5])<<24 | int64(data[pos+6])<<16 |
			int64(data[pos+7])<<8 | int64(data[pos+8])
		return val, 9, nil
	}

	return 0, 0, fmt.Errorf("varint: unexpected byte 0x%02x", b)
}

// readVarString reads a varint-encoded string (length prefix + UTF-8 bytes).
func readVarString(data []byte, pos int) (string, int, error) {
	length, n, err := readVarInt(data, pos)
	if err != nil {
		return "", 0, err
	}
	pos += n
	strLen := int(length)
	if pos+strLen > len(data) {
		return "", 0, io.ErrUnexpectedEOF
	}
	return string(data[pos : pos+strLen]), n + strLen, nil
}
