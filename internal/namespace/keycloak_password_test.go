package namespace

import (
	"strings"
	"testing"
)

func TestGenerateSimpleAdminPassword(t *testing.T) {
	seen := make(map[string]bool)
	for range 50 {
		p, err := GenerateSimpleAdminPassword()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(p) != adminPasswordLength {
			t.Fatalf("expected length %d, got %d: %q", adminPasswordLength, len(p), p)
		}
		for _, r := range p {
			if !strings.ContainsRune(adminPasswordAlphabet, r) {
				t.Errorf("password contains char %q not in alphabet: %q", r, p)
			}
		}
		// Ambiguous characters must never appear.
		for _, ambiguous := range []rune{'0', 'O', '1', 'l', 'I'} {
			if strings.ContainsRune(p, ambiguous) {
				t.Errorf("password contains ambiguous char %q: %q", ambiguous, p)
			}
		}
		seen[p] = true
	}
	if len(seen) < 50 {
		t.Errorf("got %d unique passwords from 50 calls, expected 50 (RNG broken?)", len(seen))
	}
}

func TestGenerateCiteckSAPassword(t *testing.T) {
	seen := make(map[string]bool)
	for range 20 {
		p, err := GenerateCiteckSAPassword()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(p) != citeckSAPasswordLength {
			t.Fatalf("expected length %d, got %d", citeckSAPasswordLength, len(p))
		}
		for _, r := range p {
			if !strings.ContainsRune(adminPasswordAlphabet, r) {
				t.Errorf("password contains char %q not in alphabet", r)
			}
		}
		seen[p] = true
	}
	if len(seen) < 20 {
		t.Errorf("got %d unique SA passwords from 20 calls", len(seen))
	}
}
