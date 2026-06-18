package update

import "testing"

func TestGreater(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.5.0", "2.4.0", true},
		{"2.4.0", "2.5.0", false},
		{"2.4.0", "2.4.0", false}, // equal is NOT greater (never re-run same version)
		{"v2.5.0", "2.4.0", true}, // tolerates "v" prefix on either side
		{"2.5.0", "v2.5.0", false},
		{"2.5.0", "dev", false},   // dev current sorts HIGHEST → no self-downgrade to a release
		{"dev", "2.5.0", true},    // a dev build is newer than any real release
		{"dev", "nightly", false}, // two invalid versions compare equal → no update
		{"2.10.0", "2.9.0", true}, // numeric, not lexical
	}
	for _, c := range cases {
		if got := Greater(c.a, c.b); got != c.want {
			t.Errorf("Greater(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestIsValidVersion(t *testing.T) {
	valid := []string{"2.6.0", "v2.6.0", "2.6.0-rc1", "10.0.1"}
	invalid := []string{"", "..", "../etc", "2.6.0/../x", "dev", "nightly", "v"}
	for _, v := range valid {
		if !IsValidVersion(v) {
			t.Errorf("IsValidVersion(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if IsValidVersion(v) {
			t.Errorf("IsValidVersion(%q) = true, want false (path-unsafe / not semver)", v)
		}
	}
}
