package daemon

import (
	"bytes"
	"testing"
)

func TestStripAnsiBytes_WholeAndSplit(t *testing.T) {
	// Sample chunk that exhibits the Java/Spring SGR pattern that was leaking
	// into the log viewer when follow=true: [2m timestamps [0;39m, [32m INFO
	// level, embedded tabs.
	in := []byte("\x1b[2m2026-05-29 04:22:04.680\x1b[0;39m \x1b[32m INFO\x1b[0;39m \x1b[2m[main]\x1b[0;39m\t\x1b[36mr.c.e.r.RecordsDaoRegistrar\x1b[0;39m\n")
	want := []byte("2026-05-29 04:22:04.680  INFO [main]    r.c.e.r.RecordsDaoRegistrar\n")

	got, leftover := stripAnsiBytes(in, nil)
	if !bytes.Equal(got, want) {
		t.Fatalf("whole-pass mismatch\n  want: %q\n  got:  %q", want, got)
	}
	if len(leftover) != 0 {
		t.Fatalf("expected no leftover, got %q", leftover)
	}

	// Split the input across an ESC boundary — the parser has to carry the
	// unfinished sequence into the next call. Try every split point so a
	// future change doesn't pass the sunny day case but break boundary cases.
	for cut := 1; cut < len(in)-1; cut++ {
		a, carry := stripAnsiBytes(in[:cut], nil)
		b, leftover := stripAnsiBytes(in[cut:], carry)
		combined := append(append([]byte{}, a...), b...)
		if len(leftover) != 0 {
			t.Fatalf("cut=%d: trailing leftover %q", cut, leftover)
		}
		if !bytes.Equal(combined, want) {
			t.Fatalf("cut=%d split mismatch\n  want: %q\n  got:  %q", cut, want, combined)
		}
	}
}

func TestStripAnsiBytes_LonesAndMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"lone_esc_at_end", []byte("abc\x1b"), "abc"},
		{"esc_no_bracket", []byte("a\x1bX b"), "aX b"},
		{"unterminated_csi", []byte("a\x1b[32"), "a"},
		{"tab_replaced", []byte("a\tb"), "a    b"},
		{"empty", []byte(""), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := stripAnsiBytes(c.in, nil)
			if string(got) != c.want {
				t.Fatalf("want %q, got %q", c.want, string(got))
			}
		})
	}
}
