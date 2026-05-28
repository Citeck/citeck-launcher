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
		IssuedAt:   Time{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		ValidFrom:  Time{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		ValidUntil: Time{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
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
		ID: "x", ValidFrom: Time{from}, ValidUntil: Time{until},
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

func TestTime_MarshalMidnightUTC(t *testing.T) {
	t.Parallel()
	lt := Time{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	got, err := json.Marshal(lt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != `"2025-01-01"` {
		t.Fatalf("midnight UTC must serialize as date-only, got %s", got)
	}
}

func TestTime_MarshalNonMidnight(t *testing.T) {
	t.Parallel()
	// Non-midnight UTC → full ISO-8601 with Z (matches Java Instant.toString()).
	lt := Time{time.Date(2025, 1, 1, 12, 30, 45, 0, time.UTC)}
	got, err := json.Marshal(lt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != `"2025-01-01T12:30:45Z"` {
		t.Fatalf("non-midnight must serialize with full time, got %s", got)
	}

	// Non-UTC zone → still emit Z (Kotlin uses Instant which normalises to UTC).
	tz, _ := time.LoadLocation("America/New_York")
	lt2 := Time{time.Date(2025, 1, 1, 7, 30, 45, 0, tz)} // 12:30:45 UTC
	got2, err := json.Marshal(lt2)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got2) != `"2025-01-01T12:30:45Z"` {
		t.Fatalf("non-UTC zone must be converted to UTC Z form, got %s", got2)
	}
}

func TestTime_UnmarshalBothForms(t *testing.T) {
	t.Parallel()
	// Date-only form.
	var a Time
	if err := json.Unmarshal([]byte(`"2025-01-01"`), &a); err != nil {
		t.Fatalf("unmarshal date-only: %v", err)
	}
	want := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if !a.Equal(want) {
		t.Fatalf("date-only got %v, want %v", a, want)
	}

	// Full RFC3339 form.
	var b Time
	if err := json.Unmarshal([]byte(`"2025-01-01T12:30:45Z"`), &b); err != nil {
		t.Fatalf("unmarshal rfc3339: %v", err)
	}
	want2 := time.Date(2025, 1, 1, 12, 30, 45, 0, time.UTC)
	if !b.Equal(want2) {
		t.Fatalf("rfc3339 got %v, want %v", b, want2)
	}
}

func TestTime_RoundTripStable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
	}{
		{"date-only", `"2025-01-01"`},
		{"midnight-rfc3339-via-marshal", `"2025-06-15"`},
		{"full-time", `"2025-06-15T18:42:11Z"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var lt Time
			if err := json.Unmarshal([]byte(tc.in), &lt); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			out, err := json.Marshal(lt)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(out) != tc.in {
				t.Fatalf("round-trip mismatch: in=%s out=%s", tc.in, out)
			}
			// Second round-trip must be identical (idempotent).
			var lt2 Time
			if err2 := json.Unmarshal(out, &lt2); err2 != nil {
				t.Fatalf("second unmarshal: %v", err2)
			}
			out2, err2 := json.Marshal(lt2)
			if err2 != nil {
				t.Fatalf("second marshal: %v", err2)
			}
			if !bytes.Equal(out, out2) {
				t.Fatalf("not idempotent: %s vs %s", out, out2)
			}
		})
	}
}

// TestContentForSign_KotlinCanonicalBytes asserts the exact byte sequence
// Kotlin's getContentForSign() would produce for a license with midnight-UTC
// dates. This is the regression that motivated item #14 in REMAINING.md:
// before the Time wrapper, Go emitted full RFC3339 strings while
// Kotlin's LicenseDateSerializer stripped "T00:00:00Z", so the signed bytes
// diverged and signature verification failed.
func TestContentForSign_KotlinCanonicalBytes(t *testing.T) {
	t.Parallel()
	lic := Instance{
		ID:         "lic-1",
		Tenant:     "acme",
		Priority:   42,
		IssuedTo:   "Acme",
		IssuedAt:   Time{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		ValidFrom:  Time{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		ValidUntil: Time{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		Content:    json.RawMessage(`{"feature":"enterprise"}`),
		Signatures: []Signature{{Time: "irrelevant", Issuer: "irrelevant"}},
	}
	got, err := lic.ContentForSign()
	if err != nil {
		t.Fatalf("ContentForSign: %v", err)
	}
	want := `{"content":{"feature":"enterprise"},"id":"lic-1","issuedAt":"2025-01-01","issuedTo":"Acme","priority":42,"tenant":"acme","validFrom":"2025-01-01","validUntil":"2026-01-01"}`
	if string(got) != want {
		t.Fatalf("canonical bytes mismatch:\n got=%s\nwant=%s", got, want)
	}
}

// TestContentForSign_KotlinCanonicalBytesNonMidnight pins the alternate
// branch: when any of issuedAt/validFrom/validUntil carries a non-midnight
// time, Kotlin emits the full Instant.toString() (always Z, never +00:00).
func TestContentForSign_KotlinCanonicalBytesNonMidnight(t *testing.T) {
	t.Parallel()
	lic := Instance{
		ID:         "lic-2",
		Tenant:     "acme",
		Priority:   1,
		IssuedTo:   "Acme",
		IssuedAt:   Time{time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC)},
		ValidFrom:  Time{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		ValidUntil: Time{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		Content:    json.RawMessage(`{}`),
	}
	got, err := lic.ContentForSign()
	if err != nil {
		t.Fatalf("ContentForSign: %v", err)
	}
	want := `{"content":{},"id":"lic-2","issuedAt":"2025-01-01T09:00:00Z","issuedTo":"Acme","priority":1,"tenant":"acme","validFrom":"2025-01-01","validUntil":"2026-01-01"}`
	if string(got) != want {
		t.Fatalf("canonical bytes mismatch:\n got=%s\nwant=%s", got, want)
	}
}

// TestInstance_UnmarshalAcceptsBothDateForms ensures a Kotlin-signed license
// blob (date-only) and a richer Go-side blob (full time) both decode into
// the same time.Time semantics.
func TestInstance_UnmarshalAcceptsBothDateForms(t *testing.T) {
	t.Parallel()
	blob := []byte(`{
		"id":"x","tenant":"t","priority":1,"issuedTo":"Y",
		"issuedAt":"2025-01-01",
		"validFrom":"2025-01-01T00:00:00Z",
		"validUntil":"2026-06-15T23:59:59Z",
		"content":{},"signatures":[]
	}`)
	var lic Instance
	if err := json.Unmarshal(blob, &lic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !lic.IssuedAt.Equal(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("issuedAt date-only parse failed: %v", lic.IssuedAt)
	}
	if !lic.ValidFrom.Equal(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("validFrom RFC3339 parse failed: %v", lic.ValidFrom)
	}
	if !lic.ValidUntil.Equal(time.Date(2026, 6, 15, 23, 59, 59, 0, time.UTC)) {
		t.Fatalf("validUntil RFC3339 parse failed: %v", lic.ValidUntil)
	}
}

func TestContentForSign_IsStableAcrossCalls(t *testing.T) {
	t.Parallel()
	lic := Instance{
		ID:         "x",
		Tenant:     "t",
		Priority:   1,
		IssuedAt:   Time{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		ValidFrom:  Time{time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		ValidUntil: Time{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
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
