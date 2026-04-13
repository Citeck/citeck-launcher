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
		ctrl := int(compressed[ipos]) //nolint:gosec // bounds checked by loop condition
		ipos++

		if ctrl < 32 {
			opos, ipos = decompressLiteral(out, compressed, opos, ipos, ctrl)
			continue
		}

		var err error
		opos, ipos, err = decompressBackRef(out, compressed, opos, ipos, ctrl, decompressedLen)
		if err != nil {
			return nil, err
		}
	}

	if opos != decompressedLen {
		return nil, fmt.Errorf("lzf: decompressed %d bytes, expected %d", opos, decompressedLen)
	}

	return out, nil
}

// decompressLiteral handles a literal run: copy ctrl+1 bytes from input to output.
func decompressLiteral(out, compressed []byte, opos, ipos, ctrl int) (newOpos, newIpos int) {
	length := ctrl + 1
	copy(out[opos:], compressed[ipos:ipos+length])
	return opos + length, ipos + length
}

// decompressBackRef handles a back-reference: copy length bytes from earlier in output.
func decompressBackRef(out, compressed []byte, opos, ipos, ctrl, decompressedLen int) (newOpos, newIpos int, _ error) {
	length := (ctrl >> 5) + 2
	if ipos >= len(compressed) {
		return 0, 0, fmt.Errorf("lzf: back-reference overflows input at ipos=%d", ipos)
	}
	offset := ((ctrl & 0x1F) << 8) + int(compressed[ipos]) + 1
	ipos++

	if length == 9 {
		if ipos >= len(compressed) {
			return 0, 0, fmt.Errorf("lzf: extended length overflows input at ipos=%d", ipos)
		}
		length += int(compressed[ipos])
		ipos++
	}

	ref := opos - offset
	if ref < 0 {
		return 0, 0, fmt.Errorf("lzf: negative back-reference at opos=%d, offset=%d", opos, offset)
	}
	if opos+length > decompressedLen {
		return 0, 0, fmt.Errorf("lzf: back-reference overflows output at opos=%d, length=%d", opos, length)
	}
	// Copy byte-by-byte (overlapping allowed)
	for i := 0; i < length; i++ {
		out[opos] = out[ref]
		opos++
		ref++
	}
	return opos, ipos, nil
}
