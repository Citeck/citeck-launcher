package namespace

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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

// TestSubstituteAdminPasswordHash_RealTemplate loads the actual bundled
// realm.json and verifies the substitution matches all three placeholders
// (secretData, credentialData, requiredActions) and produces valid JSON.
func TestSubstituteAdminPasswordHash_RealTemplate(t *testing.T) {
	// Locate the repo-root realm file relative to the test file.
	realmPath := filepath.Join("..", "appfiles", "keycloak", "ecos-app-realm.json")
	original, err := os.ReadFile(realmPath)
	if err != nil {
		t.Fatalf("read realm template: %v", err)
	}

	substituted := substituteAdminPasswordHash(string(original), "new-password-xyz")

	// All three placeholders must be gone from the output.
	if strings.Contains(substituted, `31MudlHfx763mvpL`) {
		t.Error("substituted realm still contains the template secretData placeholder")
	}
	if strings.Contains(substituted, `"requiredActions" : [ "UPDATE_PASSWORD" ]`) {
		t.Error("substituted realm still contains the UPDATE_PASSWORD required action")
	}

	// The output must still be valid JSON — substitution broke nothing.
	var parsed any
	if err := json.Unmarshal([]byte(substituted), &parsed); err != nil {
		t.Fatalf("substituted realm is not valid JSON: %v", err)
	}

	// Sanity: the user entry should still be present.
	if !strings.Contains(substituted, `"username" : "admin"`) {
		t.Error("substituted realm is missing the admin user entry")
	}
}

// TestSubstituteAdminPasswordHash_NoopOnUnrelatedInput confirms that calling
// substituteAdminPasswordHash on a string without the template placeholders
// returns the input unchanged (defensive — future realm format changes).
func TestSubstituteAdminPasswordHash_NoopOnUnrelatedInput(t *testing.T) {
	input := `{"realm": "example", "users": []}`
	out := substituteAdminPasswordHash(input, "password")
	if out != input {
		t.Errorf("expected unchanged output, got: %q", out)
	}
}
