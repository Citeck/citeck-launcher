package h2migrate

import (
	"fmt"
)

// decompressLZF decompresses LZF-compressed data.
// H2 MVStore uses the LZF format for chunk compression.
// Format: each block starts with a control byte:
//   - If high bit is 0: literal run of (ctrl + 1) bytes
//   - If high bit is 1: back-reference; length = ((ctrl >> 5) & 7) + 2,
//     offset = ((ctrl & 0x1F) << 8) + next_byte + 1
//     If length == 9 (encoded as 7+2), read another byte: length = next_byte + 9
func decompressLZF(compressed []byte, decompressedLen int) ([]byte, error) {
	out := make([]byte, decompressedLen)
	ipos := 0
	opos := 0

	for ipos < len(compressed) && opos < decompressedLen {
		ctrl := int(compressed[ipos])
		ipos++

		if ctrl < 32 {
			// Literal run: copy ctrl+1 bytes
			length := ctrl + 1
			if ipos+length > len(compressed) {
				return nil, fmt.Errorf("lzf: literal run overflows input at ipos=%d, length=%d", ipos, length)
			}
			if opos+length > decompressedLen {
				return nil, fmt.Errorf("lzf: literal run overflows output at opos=%d, length=%d", opos, length)
			}
			copy(out[opos:], compressed[ipos:ipos+length])
			ipos += length
			opos += length
		} else {
			// Back-reference
			length := (ctrl >> 5) + 2
			if ipos >= len(compressed) {
				return nil, fmt.Errorf("lzf: back-reference overflows input at ipos=%d", ipos)
			}
			offset := ((ctrl & 0x1F) << 8) + int(compressed[ipos]) + 1
			ipos++

			if length == 9 {
				// Extended length
				if ipos >= len(compressed) {
					return nil, fmt.Errorf("lzf: extended length overflows input at ipos=%d", ipos)
				}
				length += int(compressed[ipos])
				ipos++
			}

			ref := opos - offset
			if ref < 0 {
				return nil, fmt.Errorf("lzf: negative back-reference at opos=%d, offset=%d", opos, offset)
			}
			if opos+length > decompressedLen {
				return nil, fmt.Errorf("lzf: back-reference overflows output at opos=%d, length=%d", opos, length)
			}
			// Copy byte-by-byte (overlapping allowed)
			for i := 0; i < length; i++ {
				out[opos] = out[ref]
				opos++
				ref++
			}
		}
	}

	if opos != decompressedLen {
		return nil, fmt.Errorf("lzf: decompressed %d bytes, expected %d", opos, decompressedLen)
	}

	return out, nil
}
