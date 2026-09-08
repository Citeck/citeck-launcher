package fsutil

import "testing"

func TestFormatBytesUsesBinaryUnitsAndOneDecimal(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{512 << 20, "512.0 MiB"},
		{1 << 30, "1.0 GiB"},
		{2560 << 20, "2.5 GiB"},
		{1 << 40, "1.0 TiB"},
		{1 << 50, "1.0 PiB"},
		{1 << 60, "1.0 EiB"},
	}
	for _, c := range cases {
		if got := FormatBytes(c.in); got != c.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A negative size is nonsense the callers cannot produce, but a formatter that
// panics or prints "-1.0 EiB" on it would turn a bad measurement into a bad
// message; bytes below one unit are always printed verbatim.
func TestFormatBytesPrintsANegativeSizeVerbatim(t *testing.T) {
	if got := FormatBytes(-5); got != "-5 B" {
		t.Errorf("FormatBytes(-5) = %q, want %q", got, "-5 B")
	}
}
