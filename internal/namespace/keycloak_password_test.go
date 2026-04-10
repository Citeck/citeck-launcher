package namespace

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/crypto/pbkdf2"
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
	// 50 draws from a 53^10 space should practically never collide. A
	// collision here would mean our RNG isn't working — fail loudly.
	if len(seen) < 50 {
		t.Errorf("got %d unique passwords from 50 calls, expected 50 (RNG broken?)", len(seen))
	}
}

func TestHashKeycloakPBKDF2_RoundTrip(t *testing.T) {
	const password = "test-password-1234"
	secretData, credentialData, err := HashKeycloakPBKDF2(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	// Verify credentialData shape.
	var cd keycloakCredentialData
	if cdErr := json.Unmarshal([]byte(credentialData), &cd); cdErr != nil {
		t.Fatalf("unmarshal credentialData: %v", cdErr)
	}
	if cd.HashIterations != kcHashIterations {
		t.Errorf("credentialData iterations = %d, want %d", cd.HashIterations, kcHashIterations)
	}
	if cd.Algorithm != kcHashAlgorithm {
		t.Errorf("credentialData algorithm = %q, want %q", cd.Algorithm, kcHashAlgorithm)
	}

	// Verify secretData round-trips: hash the password again with the
	// stored salt + iterations, must match the stored hash.
	var sd keycloakSecretData
	if sdErr := json.Unmarshal([]byte(secretData), &sd); sdErr != nil {
		t.Fatalf("unmarshal secretData: %v", sdErr)
	}
	salt, err := base64.StdEncoding.DecodeString(sd.Salt)
	if err != nil {
		t.Fatalf("decode salt: %v", err)
	}
	if len(salt) != kcSaltBytes {
		t.Errorf("salt length = %d, want %d", len(salt), kcSaltBytes)
	}
	recomputed := pbkdf2.Key([]byte(password), salt, kcHashIterations, kcHashBytes, sha256.New)
	stored, err := base64.StdEncoding.DecodeString(sd.Value)
	if err != nil {
		t.Fatalf("decode stored hash: %v", err)
	}
	if !bytes.Equal(recomputed, stored) {
		t.Errorf("recomputed hash does not match stored hash — keycloak verification would fail")
	}
}

func TestHashKeycloakPBKDF2_DistinctSalts(t *testing.T) {
	const password = "same-password"
	sd1, _, err := HashKeycloakPBKDF2(password)
	if err != nil {
		t.Fatalf("hash 1: %v", err)
	}
	sd2, _, err := HashKeycloakPBKDF2(password)
	if err != nil {
		t.Fatalf("hash 2: %v", err)
	}
	if sd1 == sd2 {
		t.Error("two hashes of the same password produced identical secretData — salt reuse")
	}
}
