package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// Release-signature verification seam — ACTIVE (embeddedSigningPubKeyHex is set
// below; see "Key activated 2026-06-10").
//
// The auto-updater verifies a downloaded tarball against its `.sha256` sidecar,
// which comes from the same origin (GitHub release assets). That gives
// integrity, not authenticity: whoever can serve the tarball can serve a
// matching checksum. Because embeddedSigningPubKeyHex is non-empty, Stage
// additionally REQUIRES a detached ed25519 signature asset "<asset>.sig" next
// to each release tarball and verifies it over the raw tarball bytes before
// extraction — so every release MUST ship .sig assets (CI signs when the
// RELEASE_SIGNING_KEY secret is set) or auto-update fails closed. Clearing the
// constant back to "" disables the seam (sha256-only, prior behavior).
//
// Enabling the seam:
//
//  1. Generate a keypair (keep the PEM private key offline / in CI secrets):
//     openssl genpkey -algorithm ed25519 -out release-signing.pem
//  2. Extract the raw 32-byte public key as hex and paste it into
//     embeddedSigningPubKeyHex below (the DER SubjectPublicKeyInfo for
//     ed25519 is 44 bytes; the last 32 are the raw key):
//     openssl pkey -in release-signing.pem -pubout -outform DER | tail -c 32 | xxd -p -c 64
//  3. Store the PEM private key as the RELEASE_SIGNING_KEY GitHub Actions
//     secret — the optional signing step in .github/workflows/release-go.yml
//     then produces "<asset>.sig" (raw 64-byte signature) for every server
//     tarball via:
//     openssl pkeyutl -sign -rawin -inkey release-signing.pem -in <asset> -out <asset>.sig
//
// Both the constant and the secret must be set together: a non-empty key with
// unsigned releases makes Stage fail (missing .sig ⇒ reject), and signed
// releases with an empty key are accepted but unverified.

// embeddedSigningPubKeyHex is the hex-encoded raw 32-byte ed25519 public key
// used to verify release signatures. Empty = signature verification disabled.
//
// Key activated 2026-06-10. From the first release built with this constant,
// every GitHub release MUST carry .sig assets (CI signs when the
// RELEASE_SIGNING_KEY secret is set) — updated binaries fail-closed on a
// missing/invalid signature. Rotation: ship a release signed with the current
// key that embeds the new key, then switch the CI secret.
const embeddedSigningPubKeyHex = "388070d687245b90e3dbde5baad3e65ef30bdf29a37e6b801d14b3996cd9ecba"

// Manual-update classification reasons surfaced through Status (and the web
// UpdateStatusDto). They mirror the sentinel errors below.
const (
	ReasonSignatureMissing  = "signature_missing"
	ReasonSignatureMismatch = "signature_mismatch"
)

// Sentinel errors classifying signature failures during Stage. Both mean
// auto-update can never succeed for the offending release with the key this
// binary embeds (e.g. after a signing-key rotation) — the remedy is a one-time
// manual download from the releases page. They are errors.Is-able through all
// the fmt.Errorf("%w") wrapping on the Stage path.
var (
	// ErrSignatureMissing — the release exists but ships no ".sig" asset.
	ErrSignatureMissing = errors.New("release has no signature asset")
	// ErrSignatureMismatch — a ".sig" asset is present but does not verify
	// under the embedded public key (key rotated, or the asset was altered).
	ErrSignatureMismatch = errors.New("release signature does not match the embedded key")
)

// ManualUpdateReason maps a Stage error to its UI-facing manual-update
// classification. An empty string means the error is NOT a signature
// classification (e.g. a transient network failure) and must not raise the
// manual-update flag.
func ManualUpdateReason(err error) string {
	switch {
	case errors.Is(err, ErrSignatureMissing):
		return ReasonSignatureMissing
	case errors.Is(err, ErrSignatureMismatch):
		return ReasonSignatureMismatch
	default:
		return ""
	}
}

// parsePublicKeyHex decodes a hex-encoded raw ed25519 public key.
// An empty string means "verification disabled" and yields (nil, nil).
func parsePublicKeyHex(pubHex string) (ed25519.PublicKey, error) {
	if pubHex == "" {
		return nil, nil
	}
	raw, err := hex.DecodeString(pubHex)
	if err != nil {
		return nil, fmt.Errorf("invalid release-signing public key hex: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("release-signing public key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// parseSignature accepts a detached signature file's content: either the raw
// 64-byte ed25519 signature (as produced by `openssl pkeyutl -sign -rawin`)
// or its hex encoding (optionally whitespace-padded).
func parseSignature(data []byte) ([]byte, error) {
	if len(data) == ed25519.SignatureSize {
		return data, nil
	}
	trimmed := bytes.TrimSpace(data)
	sig, err := hex.DecodeString(string(trimmed))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("signature must be %d raw bytes or their hex encoding (got %d bytes)", ed25519.SignatureSize, len(data))
	}
	return sig, nil
}

// verifyFileSignature checks that sig is a valid ed25519 signature by pub
// over the entire content of the file at path.
func verifyFileSignature(path string, sig []byte, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid ed25519 public key")
	}
	data, err := os.ReadFile(path) //nolint:gosec // our own download
	if err != nil {
		return fmt.Errorf("read for signature verify: %w", err)
	}
	if !ed25519.Verify(pub, data, sig) {
		return fmt.Errorf("%w (key rotated or asset altered)", ErrSignatureMismatch)
	}
	return nil
}

// fetchSignature downloads the asset's detached ".sig" sidecar and parses it.
func fetchSignature(ctx context.Context, c *client, tag, asset string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "sig-*")
	if err != nil {
		return nil, fmt.Errorf("temp for sig: %w", err)
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpName)
	if dlErr := c.downloadFile(ctx, c.assetURL(tag, asset+".sig"), tmpName); dlErr != nil {
		if errors.Is(dlErr, errNotFound) {
			// The release is reachable but ships no .sig — a permanent,
			// classified condition (not a transient fetch problem).
			return nil, fmt.Errorf("%w: %s.sig not published for %s", ErrSignatureMissing, asset, tag)
		}
		return nil, fmt.Errorf("fetch signature sidecar: %w", dlErr) // transient (network etc.)
	}
	data, err := os.ReadFile(tmpName) //nolint:gosec // our own temp
	if err != nil {
		return nil, fmt.Errorf("read sig sidecar: %w", err)
	}
	sig, err := parseSignature(data)
	if err != nil {
		// A sidecar that exists but can never verify counts as a mismatch.
		return nil, fmt.Errorf("%w: %w", ErrSignatureMismatch, err)
	}
	return sig, nil
}

// verifyReleaseSignature enforces the seam for one downloaded tarball: when
// the service has a signing key configured, the matching ".sig" asset is
// required and must verify over the tarball bytes. With no key configured it
// is a no-op (legacy sha256-only behavior).
func (s *Service) verifyReleaseSignature(ctx context.Context, c *client, tag, asset, targz string) error {
	pub, err := parsePublicKeyHex(s.signingPubKeyHex)
	if err != nil {
		return err // malformed embedded key: fail closed, never skip
	}
	if pub == nil {
		return nil // seam dormant
	}
	sig, err := fetchSignature(ctx, c, tag, asset)
	if err != nil {
		return err
	}
	return verifyFileSignature(targz, sig, pub)
}
