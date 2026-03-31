package acme

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	goacme "golang.org/x/crypto/acme"
)

// authorizeOrderWithProfile creates an ACME order with a profile field.
// This is needed for IP address certificates which require the "shortlived" profile.
// The Go acme library doesn't support profiles yet, so we implement JWS signing manually.
func authorizeOrderWithProfile(ctx context.Context, client *goacme.Client, ids []goacme.AuthzID, profile string) (*goacme.Order, error) {
	// Get directory to find newOrder URL
	dir, err := client.Discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover: %w", err)
	}

	// Get account URL (KID)
	acct, err := client.GetReg(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}

	// Build order request with profile
	type identifier struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	orderReq := struct {
		Identifiers []identifier `json:"identifiers"`
		Profile     string       `json:"profile,omitempty"`
	}{
		Profile: profile,
	}
	for _, id := range ids {
		orderReq.Identifiers = append(orderReq.Identifiers, identifier{
			Type:  id.Type,
			Value: id.Value,
		})
	}

	payload, err := json.Marshal(orderReq)
	if err != nil {
		return nil, err
	}

	// Get nonce
	nonce, err := fetchNonce(dir.NonceURL)
	if err != nil {
		return nil, fmt.Errorf("fetch nonce: %w", err)
	}

	// Sign with JWS (ES256 with KID)
	key, ok := client.Key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("unsupported key type, need ECDSA")
	}

	jws, err := signJWSWithKID(key, acct.URI, nonce, dir.OrderURL, payload)
	if err != nil {
		return nil, fmt.Errorf("sign JWS: %w", err)
	}

	// POST to newOrder
	req, err := http.NewRequestWithContext(ctx, "POST", dir.OrderURL, bytes.NewReader(jws))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/jose+json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 201 {
		return nil, fmt.Errorf("order creation failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	// Parse order response
	var orderResp struct {
		Status         string       `json:"status"`
		Identifiers    []identifier `json:"identifiers"`
		Authorizations []string     `json:"authorizations"`
		Finalize       string       `json:"finalize"`
	}
	if err := json.Unmarshal(body, &orderResp); err != nil {
		return nil, fmt.Errorf("parse order: %w", err)
	}

	orderURI := resp.Header.Get("Location")

	return &goacme.Order{
		URI:         orderURI,
		Status:      orderResp.Status,
		AuthzURLs:   orderResp.Authorizations,
		FinalizeURL: orderResp.Finalize,
	}, nil
}

// fetchNonce gets a fresh nonce from the ACME server.
func fetchNonce(nonceURL string) (string, error) {
	resp, err := http.Head(nonceURL) //nolint:gosec // nonceURL comes from ACME directory discovery, not user input
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	return resp.Header.Get("Replay-Nonce"), nil
}

// signJWSWithKID creates a JWS with KID header (for existing accounts).
func signJWSWithKID(key *ecdsa.PrivateKey, kid, nonce, url string, payload []byte) ([]byte, error) {
	// Protected header
	header := map[string]string{
		"alg":   "ES256",
		"kid":   kid,
		"nonce": nonce,
		"url":   url,
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)

	// Sign
	signingInput := headerB64 + "." + payloadB64
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		return nil, err
	}

	// ES256 signature: r and s as 32-byte big-endian integers concatenated
	curveBits := key.Curve.Params().BitSize
	keyBytes := curveBits / 8
	if curveBits%8 > 0 {
		keyBytes++
	}
	sig := make([]byte, 2*keyBytes)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[keyBytes-len(rBytes):keyBytes], rBytes)
	copy(sig[2*keyBytes-len(sBytes):], sBytes)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	// Flattened JWS
	jws := map[string]string{
		"protected": headerB64,
		"payload":   payloadB64,
		"signature": sigB64,
	}
	return json.Marshal(jws)
}

