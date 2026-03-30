package h2migrate

import (
	"testing"
)

func TestReadVarIntSingleByte(t *testing.T) {
	tests := []struct {
		input []byte
		want  int64
		wantN int
	}{
		{[]byte{0}, 0, 1},
		{[]byte{1}, 1, 1},
		{[]byte{42}, 42, 1},
		{[]byte{127}, 127, 1},
	}
	for _, tt := range tests {
		got, n, err := readVarInt(tt.input, 0)
		if err != nil {
			t.Errorf("readVarInt(%v) error: %v", tt.input, err)
			continue
		}
		if got != tt.want || n != tt.wantN {
			t.Errorf("readVarInt(%v) = (%d, %d), want (%d, %d)", tt.input, got, n, tt.want, tt.wantN)
		}
	}
}

func TestReadVarIntLEB128(t *testing.T) {
	// LEB128: 0x80 | low7, high7 → value = (high7 << 7) | low7
	// 128 = 0x80 → encoded as [0x80, 0x01] (low 7 bits = 0, continuation; then 1)
	data := []byte{0x80, 0x01}
	got, n, err := readVarInt(data, 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 128 || n != 2 {
		t.Errorf("got (%d, %d), want (128, 2)", got, n)
	}

	// 300 = 0x12C → encoded as [0xAC, 0x02] (0x2C | 0x80, 0x02)
	data = []byte{0xAC, 0x02}
	got, n, err = readVarInt(data, 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 300 || n != 2 {
		t.Errorf("got (%d, %d), want (300, 2)", got, n)
	}
}

func TestReadVarString(t *testing.T) {
	// length=5 (single byte varint) + "hello"
	data := []byte{5, 'h', 'e', 'l', 'l', 'o'}
	got, n, err := readVarString(data, 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != "hello" || n != 6 {
		t.Errorf("readVarString() = (%q, %d), want (\"hello\", 6)", got, n)
	}
}
