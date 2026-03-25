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

func TestReadVarIntTwoBytes(t *testing.T) {
	// 10xxxxxx: 2 bytes, 14 bits
	// 0x80 | (high 6 bits), low 8 bits
	data := []byte{0x80 | 0x01, 0x00} // value = 256
	got, n, err := readVarInt(data, 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 256 || n != 2 {
		t.Errorf("got (%d, %d), want (256, 2)", got, n)
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
