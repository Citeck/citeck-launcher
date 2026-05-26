package license

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestContentForSign_DropsSignaturesAndSortsKeys(t *testing.T) {
	t.Parallel()
	lic := Instance{
		ID:         "lic-1",
		Tenant:     "acme",
		Priority:   10,
		IssuedTo:   "Acme Corp",
		IssuedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidFrom:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Content:    json.RawMessage(`{"z":1,"a":{"y":2,"b":[3,2,1]}}`),
		Signatures: []Signature{{
			Time:   "2025-01-01T00:00:00Z",
			Issuer: "CN=Citeck CA",
		}},
	}

	got, err := lic.ContentForSign()
	if err != nil {
		t.Fatalf("ContentForSign: %v", err)
	}

	// Sanity: must NOT contain "signatures".
	if bytes.Contains(got, []byte(`"signatures"`)) {
		t.Fatalf("signatures key was not stripped: %s", got)
	}
	// Sanity: arrays must preserve order; nested objects must be sorted.
	if !bytes.Contains(got, []byte(`"content":{"a":{"b":[3,2,1],"y":2},"z":1}`)) {
		t.Fatalf("content not canonicalised correctly: %s", got)
	}
	// Top-level keys must be lex-ordered: "content" before "id" before "issuedAt"...
	str := string(got)
	if !strings.HasPrefix(str, `{"content":`) {
		t.Fatalf("expected leading key 'content', got %s", str[:40])
	}
}

func TestValidAt_BoundaryConditions(t *testing.T) {
	t.Parallel()
	from := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	lic := Instance{
		ID: "x", ValidFrom: from, ValidUntil: until,
		Signatures: []Signature{{Time: "2025-06-01T00:00:00Z", Issuer: "x"}}, // 1 sig — IsValid still false (no cert) but boundaries check fires
	}

	// Before validFrom → false (boundary check returns before verifying).
	if lic.ValidAt(from.Add(-time.Second)) {
		t.Fatalf("license should not be valid before validFrom")
	}
	// After validUntil → false.
	if lic.ValidAt(until.Add(time.Second)) {
		t.Fatalf("license should not be valid after validUntil")
	}
	// No signatures → false.
	emptySigs := lic
	emptySigs.Signatures = nil
	if emptySigs.ValidAt(from.Add(time.Hour)) {
		t.Fatalf("license without signatures should not be valid")
	}
}

func TestContentForSign_IsStableAcrossCalls(t *testing.T) {
	t.Parallel()
	lic := Instance{
		ID:         "x",
		Tenant:     "t",
		Priority:   1,
		IssuedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidFrom:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidUntil: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Content:    json.RawMessage(`{"k":"v"}`),
	}
	a, err := lic.ContentForSign()
	if err != nil {
		t.Fatal(err)
	}
	b, err := lic.ContentForSign()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("ContentForSign is not deterministic:\n%s\nvs\n%s", a, b)
	}
}
