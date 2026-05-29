// Package license ports the Kotlin LicenseInstance / LicenseSignature pair
// and the canonical signing-form algorithm. Goal: licenses signed by the
// existing Citeck signing infrastructure (Kotlin 1.x) must verify byte-exact
// here. Any deviation in canonical-form serialization invalidates existing
// licenses.
package license

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Time wraps time.Time so Instance dates serialize compatibly with
// Kotlin's LicenseDateSerializer: midnight-UTC values emit "YYYY-MM-DD",
// everything else uses ISO-8601 with a `Z` suffix (Java Instant.toString()).
// Embedding time.Time preserves caller ergonomics (.After/.Before/.IsZero/...).
type Time struct {
	time.Time
}

// MarshalJSON serializes in Kotlin's LicenseDateSerializer format.
func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte(`""`), nil
	}
	utc := t.UTC()
	if utc.Hour() == 0 && utc.Minute() == 0 && utc.Second() == 0 && utc.Nanosecond() == 0 {
		return []byte(`"` + utc.Format("2006-01-02") + `"`), nil
	}
	// Java's Instant.toString() emits e.g. "2025-01-01T12:30:45Z" or with
	// fractional seconds "2025-01-01T12:30:45.123456789Z" — never a numeric
	// offset. Go's RFC3339 / RFC3339Nano use the same shape for UTC.
	var s string
	if utc.Nanosecond() == 0 {
		s = utc.Format("2006-01-02T15:04:05Z")
	} else {
		s = utc.Format("2006-01-02T15:04:05.999999999Z")
	}
	return []byte(`"` + s + `"`), nil
}

// UnmarshalJSON accepts both "YYYY-MM-DD" (Kotlin's compact form) and full
// RFC3339, matching the canonical wire format produced by either runtime.
func (t *Time) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("license date: %w", err)
	}
	if s == "" {
		t.Time = time.Time{}
		return nil
	}
	if len(s) == 10 {
		// Date-only form.
		parsed, err := time.ParseInLocation("2006-01-02", s, time.UTC)
		if err != nil {
			return fmt.Errorf("license date %q: %w", s, err)
		}
		t.Time = parsed
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("license date %q: %w", s, err)
	}
	t.Time = parsed.UTC()
	return nil
}

// Instance is a single license record. Fields mirror Kotlin's LicenseInstance
// in src/main/kotlin/ru/citeck/launcher/core/license/LicenseInstance.kt.
//
// The on-wire JSON shape is deliberately compatible with the Kotlin one,
// including the "date-only" Instant serialization: a value of "2025-01-01"
// is parsed as the start of that day in UTC.
type Instance struct {
	ID         string          `json:"id"`
	Tenant     string          `json:"tenant"`
	Priority   int64           `json:"priority"`
	IssuedTo   string          `json:"issuedTo"`
	IssuedAt   Time            `json:"issuedAt"`
	ValidFrom  Time            `json:"validFrom"`
	ValidUntil Time            `json:"validUntil"`
	Content    json.RawMessage `json:"content"`
	Signatures []Signature     `json:"signatures"`
}

// Signature is one signature on a license. Mirrors LicenseSignature.kt.
//
// `Signature` and `Certificates` are raw bytes; on the wire they are base64
// strings (Go's encoding/json default for []byte).
type Signature struct {
	Time         string   `json:"time"`
	Issuer       string   `json:"issuer"`
	Signature    []byte   `json:"signature"`
	Certificates [][]byte `json:"certificates"`
}

// IsValid mirrors Kotlin's LicenseInstance.isValid():
//
//  1. At least one signature is present
//  2. now is between validFrom and validUntil (inclusive on both ends)
//  3. At least one signature verifies against the canonical signing form
func (l Instance) IsValid() bool {
	return l.ValidAt(time.Now())
}

// ValidAt is the testable form of IsValid that takes the clock as a
// parameter. The boundaries match Kotlin's `validFrom.isAfter(now)` /
// `validUntil.isBefore(now)`: only the inclusive ends are considered valid.
func (l Instance) ValidAt(now time.Time) bool {
	if len(l.Signatures) == 0 {
		return false
	}
	if l.ValidFrom.After(now) || l.ValidUntil.Before(now) {
		return false
	}
	contentToSign, err := l.ContentForSign()
	if err != nil {
		return false
	}
	for _, sig := range l.Signatures {
		ok, _ := verifySignature(contentToSign, sig)
		if ok {
			return true
		}
	}
	return false
}

// ContentForSign returns the canonical bytes that were (or should be) signed:
// the license serialized to JSON, "signatures" removed, then all object keys
// sorted lexicographically at every nesting level. Arrays preserve order.
//
// This must match Kotlin's LicenseInstance.getContentForSign(); any
// difference in JSON encoding (number formatting, whitespace, key ordering)
// invalidates existing licenses.
func (l Instance) ContentForSign() ([]byte, error) {
	// Step 1: serialize via the same JSON shape callers expect.
	raw, err := json.Marshal(l)
	if err != nil {
		return nil, fmt.Errorf("marshal license: %w", err)
	}

	// Step 2: decode into a generic tree so we can drop "signatures" and
	// canonicalise key order without reflection.
	var tree any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep integral / fractional fidelity
	if decErr := dec.Decode(&tree); decErr != nil {
		return nil, fmt.Errorf("decode license: %w", decErr)
	}
	obj, ok := tree.(map[string]any)
	if !ok {
		return nil, errors.New("license JSON is not an object")
	}
	delete(obj, "signatures")

	// Step 3: re-encode with lex-sorted keys at every level.
	var buf bytes.Buffer
	if writeErr := writeCanonical(&buf, obj); writeErr != nil {
		return nil, writeErr
	}
	return buf.Bytes(), nil
}

// writeCanonical writes the given JSON-decoded value to w with all object
// keys sorted alphabetically. It deliberately uses encoding/json on scalars
// so number/string/bool/null formatting matches Kotlin Jackson exactly.
func writeCanonical(w *bytes.Buffer, v any) error {
	switch tv := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(tv))
		for k := range tv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := writeJSONString(w, k); err != nil {
				return err
			}
			w.WriteByte(':')
			if err := writeCanonical(w, tv[k]); err != nil {
				return err
			}
		}
		w.WriteByte('}')
		return nil
	case []any:
		w.WriteByte('[')
		for i, item := range tv {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := writeCanonical(w, item); err != nil {
				return err
			}
		}
		w.WriteByte(']')
		return nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal value: %w", err)
		}
		w.Write(b)
		return nil
	}
}

func writeJSONString(w *bytes.Buffer, s string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal string: %w", err)
	}
	w.Write(b)
	return nil
}

// verifySignature checks one signature against the canonical content.
// Returns (true, nil) on success; (false, reason) on auth failure; (false,
// err) on internal error.
func verifySignature(content []byte, sig Signature) (bool, error) {
	if len(sig.Certificates) == 0 {
		return false, errors.New("signature has no certificates")
	}
	cert, err := x509.ParseCertificate(sig.Certificates[0])
	if err != nil {
		return false, fmt.Errorf("parse signing cert: %w", err)
	}
	if !issuerMatches(cert, sig.Issuer) {
		return false, fmt.Errorf("issuer mismatch: cert=%q sig=%q",
			cert.Issuer.String(), sig.Issuer)
	}
	// Data signed = canonical content bytes ++ ISO8601 time bytes.
	// This matches Kotlin: signature.update(data); signature.update(time.toByteArray(UTF_8)).
	data := make([]byte, 0, len(content)+len(sig.Time))
	data = append(data, content...)
	data = append(data, []byte(sig.Time)...)
	if err := cert.CheckSignature(cert.SignatureAlgorithm, data, sig.Signature); err != nil {
		return false, fmt.Errorf("check signature: %w", err)
	}
	return true, nil
}

// issuerMatches returns true if the certificate's X.500 issuer DN matches
// `expected`. Kotlin uses Principal.toString() which produces a comma-
// separated RDN list (e.g. "CN=Citeck CA, O=Citeck"). Go's
// pkix.Name.String() emits the same format but sometimes with reordered RDN
// components when the cert is malformed; we accept either order.
func issuerMatches(cert *x509.Certificate, expected string) bool {
	got := cert.Issuer.String()
	if got == expected {
		return true
	}
	// Tolerate trailing/leading whitespace differences.
	if strings.TrimSpace(got) == strings.TrimSpace(expected) {
		return true
	}
	return false
}
