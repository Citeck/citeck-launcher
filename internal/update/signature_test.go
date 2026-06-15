package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func testKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestParsePublicKeyHex(t *testing.T) {
	pub, _ := testKeypair(t)

	t.Run("empty means dormant", func(t *testing.T) {
		got, err := parsePublicKeyHex("")
		if err != nil || got != nil {
			t.Fatalf("empty key: got %v, err %v; want nil, nil", got, err)
		}
	})
	t.Run("valid hex round-trips", func(t *testing.T) {
		got, err := parsePublicKeyHex(hex.EncodeToString(pub))
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(pub) {
			t.Fatal("decoded key differs from original")
		}
	})
	t.Run("invalid hex rejected", func(t *testing.T) {
		if _, err := parsePublicKeyHex("not-hex"); err == nil {
			t.Fatal("want error for non-hex key")
		}
	})
	t.Run("wrong length rejected", func(t *testing.T) {
		if _, err := parsePublicKeyHex("deadbeef"); err == nil {
			t.Fatal("want error for 4-byte key")
		}
	})
}

func TestParseSignature(t *testing.T) {
	pub, priv := testKeypair(t)
	msg := []byte("payload")
	sig := ed25519.Sign(priv, msg)

	t.Run("raw 64 bytes", func(t *testing.T) {
		got, err := parseSignature(sig)
		if err != nil {
			t.Fatal(err)
		}
		if !ed25519.Verify(pub, msg, got) {
			t.Fatal("raw signature did not verify after parse")
		}
	})
	t.Run("hex with trailing newline", func(t *testing.T) {
		got, err := parseSignature([]byte(hex.EncodeToString(sig) + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if !ed25519.Verify(pub, msg, got) {
			t.Fatal("hex signature did not verify after parse")
		}
	})
	t.Run("garbage rejected", func(t *testing.T) {
		if _, err := parseSignature([]byte("nonsense")); err == nil {
			t.Fatal("want error for garbage signature")
		}
	})
	t.Run("truncated raw rejected", func(t *testing.T) {
		if _, err := parseSignature(sig[:32]); err == nil {
			t.Fatal("want error for truncated signature")
		}
	})
}

func TestVerifyFileSignature(t *testing.T) {
	pub, priv := testKeypair(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "asset.tar.gz")
	content := []byte("release tarball bytes")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, content)

	t.Run("valid signature passes", func(t *testing.T) {
		if err := verifyFileSignature(path, sig, pub); err != nil {
			t.Fatalf("valid signature rejected: %v", err)
		}
	})
	t.Run("tampered file fails", func(t *testing.T) {
		tampered := filepath.Join(dir, "tampered.tar.gz")
		if err := os.WriteFile(tampered, append(content, 'X'), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := verifyFileSignature(tampered, sig, pub); err == nil {
			t.Fatal("tampered file accepted")
		}
	})
	t.Run("wrong key fails", func(t *testing.T) {
		otherPub, _ := testKeypair(t)
		if err := verifyFileSignature(path, sig, otherPub); err == nil {
			t.Fatal("signature accepted under wrong key")
		}
	})
	t.Run("invalid key fails closed", func(t *testing.T) {
		if err := verifyFileSignature(path, sig, ed25519.PublicKey([]byte{1, 2, 3})); err == nil {
			t.Fatal("malformed key accepted")
		}
	})
}

// TestEmbeddedSigningKeyParses guards the dormant seam: whatever value is
// embedded must parse (empty = dormant is fine; a malformed constant would
// make every Stage fail closed, which should be caught at test time).
func TestEmbeddedSigningKeyParses(t *testing.T) {
	if _, err := parsePublicKeyHex(embeddedSigningPubKeyHex); err != nil {
		t.Fatalf("embeddedSigningPubKeyHex is malformed: %v", err)
	}
}
