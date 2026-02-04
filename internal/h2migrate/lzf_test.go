package h2migrate

import (
	"bytes"
	"testing"
)

func TestDecompressLZFLiteral(t *testing.T) {
	// Simple literal run: ctrl=4 (length=5), followed by 5 bytes
	compressed := []byte{4, 'H', 'e', 'l', 'l', 'o'}
	got, err := decompressLZF(compressed, 5)
	if err != nil {
		t.Fatalf("decompressLZF() error: %v", err)
	}
	if !bytes.Equal(got, []byte("Hello")) {
		t.Errorf("got %q, want %q", got, "Hello")
	}
}

func TestDecompressLZFBackref(t *testing.T) {
	// "abcabc" compressed as:
	// literal "abc" (ctrl=2, then 'a','b','c')
	// then back-reference: length=3 (encoded as 1 in ctrl=0x20|0=0x20), offset=3 (encoded as 2 in next byte)
	compressed := []byte{
		2, 'a', 'b', 'c', // literal: 3 bytes
		0x20, 2, // back-ref: length=(0>>5)+2=2... wait
	}
	// Actually length = (ctrl >> 5) + 2 = (0x20>>5)+2 = 1+2=3
	// offset = ((ctrl & 0x1F) << 8) + next + 1 = (0<<8)+2+1 = 3
	got, err := decompressLZF(compressed, 6)
	if err != nil {
		t.Fatalf("decompressLZF() error: %v", err)
	}
	if !bytes.Equal(got, []byte("abcabc")) {
		t.Errorf("got %q, want %q", got, "abcabc")
	}
}

func TestDecompressLZFEmpty(t *testing.T) {
	got, err := decompressLZF([]byte{}, 0)
	if err != nil {
		t.Fatalf("decompressLZF() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d bytes, want 0", len(got))
	}
}
