package appfiles

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShquote covers the classic bash-single-quote escaping idiom: the
// helper must produce a literal that, when passed to /bin/sh -c "echo
// <literal>", prints the original input verbatim. We cover edge cases
// that Go's %q formatting would mishandle when re-interpreted by bash:
// $, backticks, backslashes, embedded newlines, and single quotes
// themselves (which must be escaped as '\”).
func TestShquote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "''"},
		{"simple", "hello", "'hello'"},
		{"with_space", "hello world", "'hello world'"},
		{"single_quote", "it's", `'it'\''s'`},
		{"only_single_quote", "'", `''\'''`},
		{"double_quote", `say "hi"`, `'say "hi"'`},
		{"backtick", "`cmd`", "'`cmd`'"},
		{"dollar", "$HOME", "'$HOME'"},
		{"cmd_subst", "$(whoami)", "'$(whoami)'"},
		{"backslash", `a\b`, `'a\b'`},
		{"newline", "a\nb", "'a\nb'"},
		{"crlf", "a\r\nb", "'a\r\nb'"},
		{"null_byte", "a\x00b", "'a\x00b'"},
		{"all_metacharacters", `$'"` + "`" + `\;&|*?[]{}()<>!#~`, `'$'\''"` + "`" + `\;&|*?[]{}()<>!#~'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shquote(tc.in))
		})
	}
}

// TestShquote_RoundTripViaBash feeds the quoted literal through real
// /bin/sh and verifies the decoded payload equals the input. This is
// the strongest guarantee: regardless of implementation detail, bash
// must emit exactly the bytes we started with. Skipped when /bin/sh
// is unavailable (e.g. cross-compile sanity checks).
func TestShquote_RoundTripViaBash(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("/bin/sh not found in PATH")
	}
	inputs := []string{
		"",
		"plain",
		"it's",
		`"double"`,
		"$HOME",
		"$(whoami)",
		"`ls`",
		`back\slash`,
		"with\nnewline",
		"tab\there",
		"mixed '\"`$\\;&| everything",
	}
	for _, in := range inputs {
		t.Run(strings.ReplaceAll(in, "\n", "\\n"), func(t *testing.T) {
			// Use printf %s to avoid echo adding a trailing newline we'd have
			// to trim. The shquote output is embedded directly into the -c
			// argument — if the escaping is wrong, sh will interpret part of
			// the input as code and the comparison will fail.
			script := "printf %s " + shquote(in)
			out, err := exec.Command(sh, "-c", script).Output() //nolint:gosec // intentional exec of shell for round-trip test
			require.NoError(t, err, "sh -c failed for input %q", in)
			assert.Equal(t, in, string(out), "round-trip mismatch for input %q", in)
		})
	}
}
