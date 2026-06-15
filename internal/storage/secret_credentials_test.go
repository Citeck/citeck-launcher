package storage

import "testing"

// TestSecretCredentials pins the shared user/pass derivation used by both the
// daemon's registry-auth cache and the CLI's registry-auth preflight: typed
// Username wins, legacy "user:pass" packed Value splits as a fallback, and
// unusable shapes report ok=false instead of half-empty pairs.
func TestSecretCredentials(t *testing.T) {
	cases := []struct {
		name     string
		secret   Secret
		wantUser string
		wantPass string
		wantOK   bool
	}{
		{
			name:     "typed username wins, password with colons untouched",
			secret:   Secret{SecretMeta: SecretMeta{Username: "alice"}, Value: "pa:ss:wo:rd"},
			wantUser: "alice", wantPass: "pa:ss:wo:rd", wantOK: true,
		},
		{
			name:     "legacy packed user:pass splits on first colon",
			secret:   Secret{Value: "bob:sec:ret"},
			wantUser: "bob", wantPass: "sec:ret", wantOK: true,
		},
		{
			name:   "no username and no colon — unusable",
			secret: Secret{Value: "just-a-token"},
			wantOK: false,
		},
		{
			name:   "packed value with empty user half",
			secret: Secret{Value: ":pass"},
			wantOK: false,
		},
		{
			name:   "packed value with empty password half",
			secret: Secret{Value: "user:"},
			wantOK: false,
		},
		{
			name:   "typed username with empty password",
			secret: Secret{SecretMeta: SecretMeta{Username: "alice"}, Value: ""},
			wantOK: false,
		},
		{
			name:   "fully empty secret",
			secret: Secret{},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user, pass, ok := tc.secret.Credentials()
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				if user != "" || pass != "" {
					t.Errorf("not-ok result must zero the pair, got (%q, %q)", user, pass)
				}
				return
			}
			if user != tc.wantUser || pass != tc.wantPass {
				t.Errorf("Credentials() = (%q, %q), want (%q, %q)", user, pass, tc.wantUser, tc.wantPass)
			}
		})
	}
}
