package cli

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDumpWriter_RedactsEntries verifies the wiring: once a redactor is set on
// the dumpWriter, every entry written to the archive is scrubbed. This is what
// guarantees secrets harvested up front never reach the zip.
func TestDumpWriter_RedactsEntries(t *testing.T) {
	const secret = "NhpP5EXvkQ3ukKyTbRcrmFZQBEWrpNrK"
	var buf bytes.Buffer
	dw := newDumpWriter(&buf)
	r := newSecretRedactor()
	r.addValue(secret)
	r.finalize()
	dw.redactor = r

	dw.addText("logs/daemon.log", "add_user citeck "+secret)
	if err := dw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	f, err := zr.Open("logs/daemon.log")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(f)
	if strings.Contains(string(content), secret) {
		t.Fatalf("secret leaked into archive: %s", content)
	}
	if !strings.Contains(string(content), redactPlaceholder) {
		t.Fatalf("expected redaction placeholder, got: %s", content)
	}
}

// TestSecretRedactor_RedactsHarvestedValueEverywhere is the core guarantee:
// a secret harvested from one container's env (here the citeck SA password)
// must be masked wherever it appears in ANY later artifact — notably daemon.log,
// where the launcher logs `rabbitmqctl add_user citeck <pass>` in plaintext.
func TestSecretRedactor_RedactsHarvestedValueEverywhere(t *testing.T) {
	const saPass = "NhpP5EXvkQ3ukKyTbRcrmFZQBEWrpNrK"
	r := newSecretRedactor()
	r.harvestEnv([]string{
		"ECOS_WEBAPP_RABBITMQ_PASSWORD=" + saPass,
		"ECOS_WEBAPP_RABBITMQ_USERNAME=citeck",
		"RABBITMQ_DEFAULT_USER=admin",
	})
	r.finalize()

	daemonLogLine := "Running init action cmd=[rabbitmqctl add_user citeck " + saPass + "]"
	got := string(r.redact([]byte(daemonLogLine)))
	if strings.Contains(got, saPass) {
		t.Fatalf("secret leaked through redaction: %s", got)
	}
	if !strings.Contains(got, redactPlaceholder) {
		t.Fatalf("expected %q placeholder, got: %s", redactPlaceholder, got)
	}

	// The username "citeck" / "admin" are NOT secrets and must survive — the
	// dump is useless if every identifier is masked.
	if out := string(r.redact([]byte("user=citeck tag=admin"))); !strings.Contains(out, "citeck") || !strings.Contains(out, "admin") {
		t.Fatalf("non-secret identifiers were redacted: %s", out)
	}
}

// TestSecretRedactor_KeyHeuristic pins which env KEYS are treated as
// secret-bearing. We'd rather over-redact a value than leak one, but innocuous
// keys (PGP key IDs, ports, usernames, BYPASS_*) must not be swept up.
func TestSecretRedactor_KeyHeuristic(t *testing.T) {
	secret := []string{
		"FOO_PASSWORD", "X_PASS", "Y_SECRET", "Z_TOKEN", "DB_CREDENTIALS",
		"TLS_PRIVATE_KEY", "S3_ACCESS_KEY", "ECOS_WEBAPP_WEB_AUTHENTICATORS_JWT_SECRET",
		"KC_BOOTSTRAP_ADMIN_PASSWORD",
	}
	notSecret := []string{
		"RABBITMQ_PGP_KEY_ID", "BYPASS_MODE", "PUBLIC_KEY_PATH",
		"RABBITMQ_DEFAULT_USER", "SERVER_PORT", "ECOS_WEBAPP_RABBITMQ_HOST",
	}
	for _, k := range secret {
		if !secretEnvKeyRe.MatchString(k) {
			t.Errorf("%q should match the secret-key heuristic", k)
		}
	}
	for _, k := range notSecret {
		if secretEnvKeyRe.MatchString(k) {
			t.Errorf("%q should NOT match the secret-key heuristic", k)
		}
	}
}

// TestSecretRedactor_SkipsTrivialValues ensures we don't harvest short or
// boolean-ish values whose blanket replacement would corrupt logs without
// protecting anything (e.g. RABBITMQ_MANAGEMENT_ALLOW_WEB_ACCESS=true).
func TestSecretRedactor_SkipsTrivialValues(t *testing.T) {
	r := newSecretRedactor()
	r.harvestEnv([]string{
		"X_SECRET=true",
		"Y_PASSWORD=admin",
		"Z_TOKEN=",
		"W_PASS=12",
	})
	r.finalize()
	if r.count() != 0 {
		t.Fatalf("trivial values were harvested as secrets: %d", r.count())
	}
	if out := string(r.redact([]byte("status=true user=admin"))); out != "status=true user=admin" {
		t.Fatalf("trivial values must not be redacted: %s", out)
	}
}

// TestSecretRedactor_RedactsLongestFirst guards against a short secret that is
// a substring of a longer one leaving the longer one partially exposed.
func TestSecretRedactor_RedactsLongestFirst(t *testing.T) {
	r := newSecretRedactor()
	r.addValue("abcdef")         // short secret
	r.addValue("abcdef12345XYZ") // longer secret containing the short one
	r.finalize()
	out := string(r.redact([]byte("a=abcdef12345XYZ b=abcdef")))
	if strings.Contains(out, "abcdef") {
		t.Fatalf("a secret value survived redaction: %s", out)
	}
}

// A value the same container publishes in the clear is not a secret, and
// masking it destroys the dump.
//
// Measured on a real stand: the launcher gives its internal databases a
// password equal to their user name ("postgres", "observer"), so every artifact
// in the archive came back with `***REDACTED***:17.5` for an image and
// `***REDACTED***-***REDACTED***` for a container name — including the
// migration report, whose whole job is to be readable.
func TestAPublishedValueIsNotTreatedAsASecret(t *testing.T) {
	r := newSecretRedactor()
	r.harvestEnv([]string{
		"POSTGRES_USER=observer",
		"POSTGRES_DB=observer",
		"POSTGRES_PASSWORD=observer",
		"KC_BOOTSTRAP_ADMIN_PASSWORD=7f3Kq2Lm9XvTb1Rn",
	})
	r.finalize()

	out := string(r.redact([]byte("observer-postgres runs postgres:18.1 for observer; admin pass 7f3Kq2Lm9XvTb1Rn")))
	assert.Contains(t, out, "observer-postgres runs postgres:18.1 for observer",
		"a word the container itself advertises must survive")
	assert.NotContains(t, out, "7f3Kq2Lm9XvTb1Rn", "a real secret is still masked")
	assert.Contains(t, out, redactPlaceholder)
}

// The rule must not weaken redaction for a secret that merely LOOKS ordinary:
// only an exact match with something published in the clear is spared.
func TestOnlyAnExactPublishedValueIsSpared(t *testing.T) {
	r := newSecretRedactor()
	r.harvestEnv([]string{
		"POSTGRES_USER=observer",
		"POSTGRES_PASSWORD=observer-secret",
	})
	r.finalize()

	out := string(r.redact([]byte("user observer with password observer-secret")))
	assert.Contains(t, out, "user observer with password "+redactPlaceholder)
}
