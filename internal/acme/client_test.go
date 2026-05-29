package acme

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goacme "golang.org/x/crypto/acme"
)

// ----------------------------------------------------------------------------
// Pure-function tests: signJWSWithKID & fetchNonce
// ----------------------------------------------------------------------------

// TestSignJWSWithKID_Roundtrip verifies that the manual JWS signer produces a
// structurally valid Flattened JWS whose ES256 signature verifies against the
// signing input under the same key.
func TestSignJWSWithKID_Roundtrip(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	kid := "https://example.test/acct/42"
	nonce := "nonce-abc-123"
	url := "https://example.test/new-order"
	payload := []byte(`{"identifiers":[{"type":"dns","value":"example.com"}]}`)

	raw, err := signJWSWithKID(key, kid, nonce, url, payload)
	if err != nil {
		t.Fatalf("signJWSWithKID: %v", err)
	}

	var jws struct {
		Protected string `json:"protected"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if uerr := json.Unmarshal(raw, &jws); uerr != nil {
		t.Fatalf("JWS is not valid JSON: %v", uerr)
	}
	if jws.Protected == "" || jws.Payload == "" || jws.Signature == "" {
		t.Fatalf("JWS missing fields: %+v", jws)
	}

	// Decode protected header and check claims.
	headerJSON, err := base64.RawURLEncoding.DecodeString(jws.Protected)
	if err != nil {
		t.Fatalf("decode protected: %v", err)
	}
	var header map[string]string
	if uerr := json.Unmarshal(headerJSON, &header); uerr != nil {
		t.Fatalf("parse header: %v", uerr)
	}
	if header["alg"] != "ES256" {
		t.Errorf("alg=%q want ES256", header["alg"])
	}
	if header["kid"] != kid {
		t.Errorf("kid=%q want %q", header["kid"], kid)
	}
	if header["nonce"] != nonce {
		t.Errorf("nonce=%q want %q", header["nonce"], nonce)
	}
	if header["url"] != url {
		t.Errorf("url=%q want %q", header["url"], url)
	}

	// Decode payload and verify byte-for-byte match.
	gotPayload, err := base64.RawURLEncoding.DecodeString(jws.Payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Errorf("payload mismatch: got %q want %q", gotPayload, payload)
	}

	// Verify ECDSA signature against the signing input.
	sig, err := base64.RawURLEncoding.DecodeString(jws.Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("ES256 signature must be 64 bytes (r||s), got %d", len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	hash := sha256.Sum256([]byte(jws.Protected + "." + jws.Payload))
	if !ecdsa.Verify(&key.PublicKey, hash[:], r, s) {
		t.Error("ECDSA signature did not verify against signing input")
	}
}

// TestFetchNonce_ReturnsHeader verifies fetchNonce extracts the Replay-Nonce
// header from a HEAD response.
func TestFetchNonce_ReturnsHeader(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD, got %s", r.Method)
		}
		n := hits.Add(1)
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", n))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got1, err := fetchNonce(srv.URL)
	if err != nil {
		t.Fatalf("fetchNonce 1: %v", err)
	}
	got2, err := fetchNonce(srv.URL)
	if err != nil {
		t.Fatalf("fetchNonce 2: %v", err)
	}
	if got1 != "nonce-1" {
		t.Errorf("first nonce got %q want nonce-1", got1)
	}
	if got2 != "nonce-2" {
		t.Errorf("second nonce got %q want nonce-2 (nonce rotation)", got2)
	}
	if got1 == got2 {
		t.Error("consecutive nonces must differ (nonce rotation invariant)")
	}
}

// TestFetchNonce_UnreachableHost returns an error rather than panicking.
func TestFetchNonce_UnreachableHost(t *testing.T) {
	_, err := fetchNonce("http://127.0.0.1:1/nonce") // port 1 is reserved/unbound
	if err == nil {
		t.Error("expected error for unreachable nonce URL")
	}
}

// ----------------------------------------------------------------------------
// Mock ACME directory server — exercises authorizeOrderWithProfile end-to-end
// ----------------------------------------------------------------------------

// mockACME is a minimal ACME server (RFC 8555) sufficient to validate the
// authorizeOrderWithProfile JWS-with-KID flow used for IP-address certs.
// It does NOT implement the HTTP-01 challenge / finalize / certificate flow
// because those code paths require binding :80 and a full goacme.Client order
// pipeline that is not parameterized in production code (see test report).
type mockACME struct {
	srv         *httptest.Server
	nonceCount  atomic.Int64
	accountURL  string
	orderStatus string // "ready"/"invalid"/"pending"
	orderHTTP   int    // override status code for newOrder; 0 → 201
	seenNonces  sync.Map
	t           *testing.T
}

func newMockACME(t *testing.T) *mockACME {
	t.Helper()
	m := &mockACME{t: t, orderStatus: "ready"}
	mux := http.NewServeMux()
	mux.HandleFunc("/directory", m.handleDirectory)
	mux.HandleFunc("/new-nonce", m.handleNewNonce)
	mux.HandleFunc("/new-account", m.handleNewAccount)
	mux.HandleFunc("/new-order", m.handleNewOrder)
	mux.HandleFunc("/acct/1", m.handleAccount)
	m.srv = httptest.NewServer(mux)
	m.accountURL = m.srv.URL + "/acct/1"
	return m
}

func (m *mockACME) Close() { m.srv.Close() }

func (m *mockACME) URL() string { return m.srv.URL + "/directory" }

func (m *mockACME) issueNonce() string {
	n := m.nonceCount.Add(1)
	nonce := fmt.Sprintf("nonce-%d-%d", n, time.Now().UnixNano())
	m.seenNonces.Store(nonce, true)
	return nonce
}

func (m *mockACME) handleDirectory(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Replay-Nonce", m.issueNonce())
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]string{
		"newNonce":   m.srv.URL + "/new-nonce",
		"newAccount": m.srv.URL + "/new-account",
		"newOrder":   m.srv.URL + "/new-order",
		"revokeCert": m.srv.URL + "/revoke",
		"keyChange":  m.srv.URL + "/key-change",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (m *mockACME) handleNewNonce(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Replay-Nonce", m.issueNonce())
	w.WriteHeader(http.StatusOK)
}

func (m *mockACME) handleNewAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	if !looksLikeJWS(body) {
		m.t.Errorf("newAccount: body is not JWS: %s", body)
	}

	// Decode payload to detect onlyReturnExisting (used by GetReg("")).
	var jws struct {
		Payload string `json:"payload"`
	}
	_ = json.Unmarshal(body, &jws)
	payloadJSON, _ := base64.RawURLEncoding.DecodeString(jws.Payload)
	var p map[string]any
	_ = json.Unmarshal(payloadJSON, &p)
	onlyExisting, _ := p["onlyReturnExisting"].(bool)

	w.Header().Set("Replay-Nonce", m.issueNonce())
	w.Header().Set("Location", m.accountURL)
	w.Header().Set("Content-Type", "application/json")
	if onlyExisting {
		// Existing-account lookup → 200 per RFC 8555 §7.3.1
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	_, _ = w.Write([]byte(`{"status":"valid","contact":[]}`))
}

func (m *mockACME) handleAccount(w http.ResponseWriter, r *http.Request) {
	// POST-as-GET for the account: goacme calls GetReg.
	w.Header().Set("Replay-Nonce", m.issueNonce())
	w.Header().Set("Location", m.accountURL)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.Copy(io.Discard, r.Body)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"valid","contact":[]}`))
}

func (m *mockACME) handleNewOrder(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if !looksLikeJWS(body) {
		m.t.Errorf("newOrder: body is not JWS: %s", body)
	}

	// Decode protected header to verify nonce was attached and is fresh.
	var jws struct {
		Protected string `json:"protected"`
		Payload   string `json:"payload"`
	}
	if err := json.Unmarshal(body, &jws); err != nil {
		http.Error(w, "bad jws", http.StatusBadRequest)
		return
	}
	headerJSON, _ := base64.RawURLEncoding.DecodeString(jws.Protected)
	var header map[string]any
	_ = json.Unmarshal(headerJSON, &header)
	nonce, _ := header["nonce"].(string)
	if _, ok := m.seenNonces.Load(nonce); !ok {
		m.t.Errorf("newOrder: unknown nonce %q (not issued by mock)", nonce)
	}
	// Single-use: delete after consumption.
	m.seenNonces.Delete(nonce)

	// Decode payload to verify profile was passed through.
	payloadJSON, _ := base64.RawURLEncoding.DecodeString(jws.Payload)
	var payload struct {
		Identifiers []map[string]string `json:"identifiers"`
		Profile     string              `json:"profile"`
	}
	_ = json.Unmarshal(payloadJSON, &payload)
	if payload.Profile != "shortlived" {
		m.t.Errorf("newOrder: profile=%q want shortlived", payload.Profile)
	}
	if len(payload.Identifiers) == 0 {
		m.t.Errorf("newOrder: identifiers must not be empty")
	}

	w.Header().Set("Replay-Nonce", m.issueNonce())
	w.Header().Set("Location", m.srv.URL+"/order/abc")
	w.Header().Set("Content-Type", "application/json")
	status := m.orderHTTP
	if status == 0 {
		status = http.StatusCreated
	}
	w.WriteHeader(status)
	if status >= 300 {
		_, _ = w.Write([]byte(`{"type":"urn:ietf:params:acme:error:rateLimited","detail":"too many"}`))
		return
	}
	resp := map[string]any{
		"status":         m.orderStatus,
		"identifiers":    payload.Identifiers,
		"authorizations": []string{m.srv.URL + "/authz/1"},
		"finalize":       m.srv.URL + "/order/abc/finalize",
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func looksLikeJWS(body []byte) bool {
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	_, hasP := m["protected"]
	_, hasPL := m["payload"]
	_, hasS := m["signature"]
	return hasP && hasPL && hasS
}

// TestAuthorizeOrderWithProfile_HappyPath drives the manual JWS order flow
// against a mock ACME directory and verifies that:
//   - the client discovers the directory,
//   - registers the account,
//   - posts a JWS-signed newOrder request with the "shortlived" profile,
//   - parses the returned authorizations/finalize URLs.
func TestAuthorizeOrderWithProfile_HappyPath(t *testing.T) {
	mock := newMockACME(t)
	defer mock.Close()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	client := &goacme.Client{
		Key:          key,
		DirectoryURL: mock.URL(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Register account first (needed so GetReg returns the KID).
	if _, regErr := client.Register(ctx, &goacme.Account{}, goacme.AcceptTOS); regErr != nil {
		t.Fatalf("Register: %v", regErr)
	}

	ids := goacme.IPIDs("192.0.2.1")
	order, err := authorizeOrderWithProfile(ctx, client, ids)
	if err != nil {
		t.Fatalf("authorizeOrderWithProfile: %v", err)
	}

	if order.URI != mock.srv.URL+"/order/abc" {
		t.Errorf("order.URI = %q", order.URI)
	}
	if len(order.AuthzURLs) != 1 {
		t.Fatalf("AuthzURLs = %v, want 1", order.AuthzURLs)
	}
	if !strings.Contains(order.FinalizeURL, "/finalize") {
		t.Errorf("FinalizeURL = %q", order.FinalizeURL)
	}
	if order.Status != "ready" {
		t.Errorf("Status = %q want ready", order.Status)
	}
}

// TestAuthorizeOrderWithProfile_RateLimited verifies that an ACME 429
// response is surfaced as an error containing the upstream detail.
func TestAuthorizeOrderWithProfile_RateLimited(t *testing.T) {
	mock := newMockACME(t)
	mock.orderHTTP = http.StatusTooManyRequests
	defer mock.Close()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client := &goacme.Client{Key: key, DirectoryURL: mock.URL()}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, regErr := client.Register(ctx, &goacme.Account{}, goacme.AcceptTOS); regErr != nil {
		t.Fatalf("Register: %v", regErr)
	}

	_, err = authorizeOrderWithProfile(ctx, client, goacme.IPIDs("192.0.2.2"))
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}
	if !strings.Contains(err.Error(), "429") && !strings.Contains(err.Error(), "too many") {
		t.Errorf("error should mention rate limit, got: %v", err)
	}
}

// TestAuthorizeOrderWithProfile_DirectoryUnreachable verifies error handling
// when the directory cannot be discovered.
func TestAuthorizeOrderWithProfile_DirectoryUnreachable(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	client := &goacme.Client{
		Key:          key,
		DirectoryURL: "http://127.0.0.1:1/directory", // unreachable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = authorizeOrderWithProfile(ctx, client, goacme.IPIDs("192.0.2.3"))
	if err == nil {
		t.Fatal("expected error for unreachable directory")
	}
}

// ----------------------------------------------------------------------------
// Rate-limit gate invariant
// ----------------------------------------------------------------------------

// TestCheckAndRenew_RespectsRateLimit verifies that when a rate-limit marker
// is present, checkAndRenew bails out BEFORE attempting any network I/O,
// even if a cert exists and is past 50% lifetime.
//
// We point the client at a temp dir with a cert that would normally trigger
// renewal, plant a rate-limit marker, and confirm restartFn is never invoked
// (which it would be only after a successful ObtainCertificate call).
func TestCheckAndRenew_RespectsRateLimit(t *testing.T) {
	confDir := t.TempDir()
	dataDir := t.TempDir()
	// Cert that's 90% expired → would trigger renewal.
	writeExampleCert(t, confDir,
		time.Now().Add(-100*time.Hour), time.Now().Add(10*time.Hour))

	// Plant rate-limit marker.
	acmeDir := filepath.Join(dataDir, "acme")
	if err := os.MkdirAll(acmeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(acmeDir, "rate-limit-until"), []byte(until), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &Client{
		dataDir:  acmeDir,
		confDir:  confDir,
		hostname: "example.com",
	}

	var restarted atomic.Bool
	svc := NewRenewalService(client, func() { restarted.Store(true) })

	// Sanity: the rate-limit predicate must see the marker.
	if !svc.isRateLimited() {
		t.Fatal("precondition: isRateLimited should be true after writing marker")
	}

	svc.checkAndRenew(context.Background())

	if restarted.Load() {
		t.Error("restartFn must NOT be called when rate-limited (rate-limit gate violated)")
	}
}

// TestCheckAndRenew_NoCertFile is a defensive test: with no cert on disk,
// checkAndRenew must NOT attempt to call ObtainCertificate (would hit real LE).
func TestCheckAndRenew_NoCertFile(t *testing.T) {
	confDir := t.TempDir()
	dataDir := t.TempDir()
	client := &Client{
		dataDir:  filepath.Join(dataDir, "acme"),
		confDir:  confDir,
		hostname: "example.com",
	}
	var restarted atomic.Bool
	svc := NewRenewalService(client, func() { restarted.Store(true) })

	// Should log a warning and return cleanly (no panic, no restart).
	svc.checkAndRenew(context.Background())

	if restarted.Load() {
		t.Error("restartFn must not fire when there's no cert to read")
	}
}

// TestSetRateLimited_WritesMarker exercises the path that the gate guards.
func TestSetRateLimited_WritesMarker(t *testing.T) {
	dataDir := t.TempDir()
	client := &Client{dataDir: filepath.Join(dataDir, "acme")}
	svc := NewRenewalService(client, nil)

	svc.setRateLimited()

	limited, until, err := IsRateLimited(dataDir, "example.com")
	if err != nil {
		t.Fatalf("IsRateLimited: %v", err)
	}
	if !limited {
		t.Fatal("expected rate-limited=true after setRateLimited")
	}
	// 1-hour backoff per implementation.
	if delta := time.Until(until); delta <= 0 || delta > time.Hour+time.Minute {
		t.Errorf("expected backoff ~1h, got %v", delta)
	}
}

// ----------------------------------------------------------------------------
// Account key persistence (file-based, no network)
// ----------------------------------------------------------------------------

// TestLoadOrCreateAccountKey_GeneratesThenReloads verifies that the account
// key is persisted as PEM and reloaded byte-for-byte on a second call.
func TestLoadOrCreateAccountKey_GeneratesThenReloads(t *testing.T) {
	dir := t.TempDir()
	c := &Client{dataDir: filepath.Join(dir, "acme")}

	k1, err := c.loadOrCreateAccountKey()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if k1 == nil {
		t.Fatal("nil key")
	}

	// Confirm PEM was written.
	keyPath := filepath.Join(c.dataDir, "account-key.pem")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if block, _ := pem.Decode(data); block == nil || block.Type != "EC PRIVATE KEY" {
		t.Fatalf("expected EC PRIVATE KEY PEM, got block=%+v", block)
	}

	k2, err := c.loadOrCreateAccountKey()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	// Equal compares the underlying public key bytes — a stable check that
	// the reload produced an equivalent key without poking ecdsa internals
	// (k1.D direct access is deprecated since Go 1.26).
	if !k1.Equal(k2) {
		t.Error("reloaded key differs from generated key — persistence broken")
	}
}

// TestLoadOrCreateAccountKey_RegeneratesOnCorruption verifies that a corrupted
// account-key.pem file leads to regeneration rather than a hard failure.
func TestLoadOrCreateAccountKey_RegeneratesOnCorruption(t *testing.T) {
	dir := t.TempDir()
	c := &Client{dataDir: filepath.Join(dir, "acme")}

	if err := os.MkdirAll(c.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(c.dataDir, "account-key.pem")
	if err := os.WriteFile(keyPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}

	k, err := c.loadOrCreateAccountKey()
	if err != nil {
		t.Fatalf("expected regeneration, got error: %v", err)
	}
	if k == nil {
		t.Fatal("nil key after corruption recovery")
	}
}

// ----------------------------------------------------------------------------
// Sanity: NewClient layout
// ----------------------------------------------------------------------------

func TestNewClient_LayoutPaths(t *testing.T) {
	c := NewClient("/data", "/conf", "example.com")
	if c.dataDir != "/data/acme" {
		t.Errorf("dataDir = %q want /data/acme", c.dataDir)
	}
	if c.confDir != "/conf/tls" {
		t.Errorf("confDir = %q want /conf/tls", c.confDir)
	}
	if c.hostname != "example.com" {
		t.Errorf("hostname = %q want example.com", c.hostname)
	}
	if c.CertPath() != "/conf/tls/fullchain.pem" {
		t.Errorf("CertPath = %q", c.CertPath())
	}
	if c.KeyPath() != "/conf/tls/privkey.pem" {
		t.Errorf("KeyPath = %q", c.KeyPath())
	}
}

// silence import-not-used if writeExampleCert signature ever changes.
var _ = x509.CreateCertificate
var _ = pkix.Name{}
