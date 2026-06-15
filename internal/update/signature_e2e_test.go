package update_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/citeck/citeck-launcher/internal/update/updatetest"
)

// These tests exercise the dormant ed25519 release-signature seam end-to-end
// through Stage against the fake GitHub (see signature.go).

func TestStageWithSignatureSeam(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)

	t.Run("signed release verifies and stages", func(t *testing.T) {
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "signed-daemon", SignKey: priv},
		)
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex(pubHex))...)
		ver, stageErr := svc.Stage(t.Context())
		if stageErr != nil {
			t.Fatalf("Stage with valid signature: %v", stageErr)
		}
		if ver != "2.6.0" {
			t.Fatalf("staged version = %q", ver)
		}
	})

	t.Run("unsigned release rejected when key configured", func(t *testing.T) {
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "unsigned-daemon"},
		)
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex(pubHex))...)
		if _, stageErr := svc.Stage(t.Context()); stageErr == nil {
			t.Fatal("Stage accepted an unsigned release with a signing key configured")
		}
	})

	t.Run("wrong key rejects signed release", func(t *testing.T) {
		otherPub, _, keyErr := ed25519.GenerateKey(rand.Reader)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "signed-daemon", SignKey: priv},
		)
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex(hex.EncodeToString(otherPub)))...)
		if _, stageErr := svc.Stage(t.Context()); stageErr == nil {
			t.Fatal("Stage accepted a signature made by a different key")
		}
	})

	t.Run("malformed embedded key fails closed", func(t *testing.T) {
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "signed-daemon", SignKey: priv},
		)
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex("zz-not-hex"))...)
		_, stageErr := svc.Stage(t.Context())
		if stageErr == nil || !strings.Contains(stageErr.Error(), "public key") {
			t.Fatalf("malformed key must fail closed, got %v", stageErr)
		}
	})

	t.Run("seam dormant without key (sig ignored)", func(t *testing.T) {
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "unsigned-daemon"},
		)
		// The production default now embeds a real key, so dormant mode must
		// be requested explicitly to pin the legacy (unverified) behavior.
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex(""))...)
		if _, stageErr := svc.Stage(t.Context()); stageErr != nil {
			t.Fatalf("dormant seam must keep legacy behavior: %v", stageErr)
		}
	})
}

// TestSignatureFailureClassification covers the manual-update flag: signature
// failures are classified (missing vs mismatch), surface through Status, are
// NOT raised by transient fetch failures, and clear on recovery (re-signed
// release or a different release appearing).
func TestSignatureFailureClassification(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPubHex := hex.EncodeToString(otherPub)

	t.Run("missing sig classified and flag raised", func(t *testing.T) {
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "unsigned-daemon"},
		)
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex(pubHex))...)
		_, stageErr := svc.Stage(t.Context())
		if !errors.Is(stageErr, update.ErrSignatureMissing) {
			t.Fatalf("want ErrSignatureMissing, got %v", stageErr)
		}
		st := svc.Status()
		if !st.ManualUpdateRequired || st.ManualUpdateReason != update.ReasonSignatureMissing {
			t.Fatalf("status = %+v; want manual update with reason %q", st, update.ReasonSignatureMissing)
		}
		wantURL := fake.URL() + "/Citeck/citeck-launcher/releases"
		if st.ReleasesURL != wantURL {
			t.Fatalf("ReleasesURL = %q, want %q", st.ReleasesURL, wantURL)
		}
	})

	t.Run("mismatch (rotated key) classified and flag raised", func(t *testing.T) {
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "signed-daemon", SignKey: priv},
		)
		// The binary embeds the OLD key; the release is signed with a new one.
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex(otherPubHex))...)
		_, stageErr := svc.Stage(t.Context())
		if !errors.Is(stageErr, update.ErrSignatureMismatch) {
			t.Fatalf("want ErrSignatureMismatch, got %v", stageErr)
		}
		st := svc.Status()
		if !st.ManualUpdateRequired || st.ManualUpdateReason != update.ReasonSignatureMismatch {
			t.Fatalf("status = %+v; want manual update with reason %q", st, update.ReasonSignatureMismatch)
		}
	})

	t.Run("transient sig fetch failure does not raise the flag", func(t *testing.T) {
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "signed-daemon", SignKey: priv, SigStatus: 500},
		)
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex(pubHex))...)
		_, stageErr := svc.Stage(t.Context())
		if stageErr == nil {
			t.Fatal("Stage must fail when the .sig fetch errors")
		}
		if errors.Is(stageErr, update.ErrSignatureMissing) || errors.Is(stageErr, update.ErrSignatureMismatch) {
			t.Fatalf("transient fetch failure must not be classified, got %v", stageErr)
		}
		if st := svc.Status(); st.ManualUpdateRequired || st.ManualUpdateReason != "" {
			t.Fatalf("transient failure raised the manual-update flag: %+v", st)
		}
	})

	t.Run("flag clears when the release is re-signed and Stage succeeds", func(t *testing.T) {
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "unsigned-daemon"},
		)
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex(pubHex))...)
		if _, stageErr := svc.Stage(t.Context()); !errors.Is(stageErr, update.ErrSignatureMissing) {
			t.Fatalf("want ErrSignatureMissing, got %v", stageErr)
		}
		if st := svc.Status(); !st.ManualUpdateRequired {
			t.Fatalf("flag not raised: %+v", st)
		}
		// The publisher fixes the release: same version, now properly signed.
		fake.SetRelease(updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "unsigned-daemon", SignKey: priv})
		if _, stageErr := svc.Stage(t.Context()); stageErr != nil {
			t.Fatalf("Stage after re-sign: %v", stageErr)
		}
		if st := svc.Status(); st.ManualUpdateRequired || st.ManualUpdateReason != "" {
			t.Fatalf("flag must clear after a successful Stage: %+v", st)
		}
	})

	t.Run("flag clears when a different release appears", func(t *testing.T) {
		fake := updatetest.Start(t, "Citeck/citeck-launcher",
			updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "unsigned-daemon"},
		)
		svc := update.NewService("2.5.0", t.TempDir(),
			append(fake.Options(), update.WithSigningPublicKeyHex(pubHex))...)
		if _, stageErr := svc.Stage(t.Context()); !errors.Is(stageErr, update.ErrSignatureMissing) {
			t.Fatalf("want ErrSignatureMissing, got %v", stageErr)
		}
		// A newer release shows up — it deserves a fresh auto attempt.
		fake.SetRelease(updatetest.Release{Version: "2.7.0", Date: "2026-06-08", BinaryContent: "signed-daemon", SignKey: priv})
		if _, checkErr := svc.CheckLatest(t.Context()); checkErr != nil {
			t.Fatal(checkErr)
		}
		if st := svc.Status(); st.ManualUpdateRequired || st.ManualUpdateReason != "" {
			t.Fatalf("flag must clear when latest changes: %+v", st)
		}
		if _, stageErr := svc.Stage(t.Context()); stageErr != nil {
			t.Fatalf("Stage of the new signed release: %v", stageErr)
		}
	})
}

// TestManualUpdateReason pins the error→reason mapping, including wrapping.
func TestManualUpdateReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"missing wrapped", fmt.Errorf("stage: %w", update.ErrSignatureMissing), update.ReasonSignatureMissing},
		{"mismatch wrapped", fmt.Errorf("stage: %w", update.ErrSignatureMismatch), update.ReasonSignatureMismatch},
		{"unrelated error", errors.New("dial tcp: connection refused"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := update.ManualUpdateReason(tc.err); got != tc.want {
				t.Fatalf("ManualUpdateReason(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
