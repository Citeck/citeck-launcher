package cli

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/citeck/citeck-launcher/internal/bundle"
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
		// SeaweedFS: the JWT signing keys, the admin UI password, the S3 key.
		"WEED_JWT_SIGNING_KEY", "WEED_JWT_SIGNING_READ_KEY",
		"WEED_JWT_FILER_SIGNING_KEY", "WEED_JWT_FILER_SIGNING_READ_KEY",
		"WEED_ADMIN_PASSWORD", "AWS_SECRET_ACCESS_KEY",
	}
	notSecret := []string{
		"RABBITMQ_PGP_KEY_ID", "BYPASS_MODE", "PUBLIC_KEY_PATH",
		"RABBITMQ_DEFAULT_USER", "SERVER_PORT", "ECOS_WEBAPP_RABBITMQ_HOST",
		// SeaweedFS settings beside its keys: a token lifetime is not a secret.
		"WEED_JWT_SIGNING_EXPIRES_AFTER_SECONDS", "WEED_JWT_FILER_SIGNING_EXPIRES_AFTER_SECONDS",
		"WEED_ADMIN_USER", "S3_BUCKET",
		// The identity half of an access key pair is a user name.
		"AWS_ACCESS_KEY_ID", "MINIO_ACCESS_KEY_ID",
	}
	for _, k := range secret {
		if !isSecretEnvKey(k) {
			t.Errorf("%q should match the secret-key heuristic", k)
		}
	}
	for _, k := range notSecret {
		if isSecretEnvKey(k) {
			t.Errorf("%q should NOT match the secret-key heuristic", k)
		}
	}
}

// The S3 store's container is configuration — the workspace's additionalApps
// entry, whose reference copy the namespace tests generate from — so the
// environment variables that carry a credential are named there, not here.
// Every one of them must be masked in a dump: a credential added to the entry
// under a name the heuristic does not know would go into the archive in the
// clear.
func TestSecretRedactor_MasksEveryCredentialOfTheS3Store(t *testing.T) {
	data, err := os.ReadFile("../namespace/testdata/content-storage-workspace.yml")
	require.NoError(t, err)
	var ws bundle.WorkspaceConfig
	require.NoError(t, yaml.Unmarshal(data, &ws))
	s3 := ws.ContentStorageS3()
	require.NotNil(t, s3)
	entry, ok := ws.AdditionalApp(s3.App)
	require.True(t, ok)

	credential := func(v string) bool {
		for _, ref := range []string{"${secret:", "${ADMIN_PASSWORD}", "${JWT_SECRET}", "${OIDC_SECRET}", "${RMQ_PASSWORD}", "${KK_ADMIN_PASSWORD}"} {
			if strings.Contains(v, ref) {
				return true
			}
		}
		return false
	}
	var checked int
	for key, value := range entry.Environments {
		if !credential(value) {
			continue
		}
		checked++
		assert.True(t, isSecretEnvKey(key), "%s carries a credential and is not masked in a dump", key)
	}
	assert.Positive(t, checked, "the store's entry names no credential at all?")
}

// SeaweedFS's keys reach the archive through its container env, and every
// copy of them is masked — wherever it appears, e.g. echoed in a log line —
// while a token lifetime and the admin user name stay readable.
func TestSecretRedactor_MasksSeaweedFSKeys(t *testing.T) {
	const (
		jwtKey   = "q3ZbV7mKc9TfR2xLpW8nHs4YdJ6aGe1Uo5Ni0Bkt"
		readKey  = "Lx2Mv9Qa7Rb4Sc1Td8Ue5Vf3Wg6Xh0Yi2Zj7Ak4"
		adminPwd = "Adm1nPassw0rdForTheUI"
		s3Key    = "Sx0Ty1Uz2Va3Wb4Xc5Yd6Ze7Af8Bg9Ch0Di1Ej2"
	)
	r := newSecretRedactor()
	r.harvestEnv([]string{
		"WEED_JWT_SIGNING_KEY=" + jwtKey,
		"WEED_JWT_SIGNING_READ_KEY=" + readKey,
		"WEED_JWT_FILER_SIGNING_KEY=" + jwtKey,
		"WEED_JWT_FILER_SIGNING_READ_KEY=" + readKey,
		"WEED_JWT_SIGNING_EXPIRES_AFTER_SECONDS=86400",
		"WEED_ADMIN_USER=seaweedadmin",
		"WEED_ADMIN_PASSWORD=" + adminPwd,
		"AWS_SECRET_ACCESS_KEY=" + s3Key,
	})
	r.finalize()
	out := string(r.redact([]byte("env: jwt=" + jwtKey + " read=" + readKey +
		" admin=" + adminPwd + " s3=" + s3Key + " ttl=86400 user=seaweedadmin")))
	for _, secret := range []string{jwtKey, readKey, adminPwd, s3Key} {
		if strings.Contains(out, secret) {
			t.Fatalf("a SeaweedFS key survived redaction: %s", out)
		}
	}
	if !strings.Contains(out, "ttl=86400") || !strings.Contains(out, "user=seaweedadmin") {
		t.Fatalf("a SeaweedFS setting was masked as a secret: %s", out)
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

// An S3 access key ID is the identity half of the pair — a user name — and
// the S3 store's is the word `citeck`, published in the workspace config.
// Harvested as a secret it masked every `citeck_<app>_<ns>` container name and
// every `harbor.citeck.ru` image in the archive, whenever no container on the
// host happened to publish `citeck` in the clear: a fresh S3 namespace that
// fails while its infrastructure is starting has the store's container and
// none of the webapps' (whose ECOS_WEBAPP_RABBITMQ_USERNAME was the only thing
// sparing it) — exactly the dump someone sends.
func TestAnAccessKeyIDIsAnIdentityNotASecret(t *testing.T) {
	data, err := os.ReadFile("../namespace/testdata/content-storage-workspace.yml")
	require.NoError(t, err)
	var ws bundle.WorkspaceConfig
	require.NoError(t, yaml.Unmarshal(data, &ws))
	entry, ok := ws.AdditionalApp(ws.ContentStorageS3().App)
	require.True(t, ok)
	accessKeyID := entry.Environments["AWS_ACCESS_KEY_ID"]
	require.Equal(t, ws.ContentStorageS3().AccessKey, accessKeyID)
	require.True(t, isLikelySecret(accessKeyID), "the case only exists while the ID is long enough to be harvested")

	const s3Key = "Sx0Ty1Uz2Va3Wb4Xc5Yd6Ze7Af8Bg9Ch0Di1Ej2"
	r := newSecretRedactor()
	// The store and the infrastructure, before any webapp container exists.
	r.harvestEnv([]string{
		"AWS_ACCESS_KEY_ID=" + accessKeyID,
		"AWS_SECRET_ACCESS_KEY=" + s3Key,
		"WEED_ADMIN_USER=admin",
		"S3_BUCKET=ecos-content",
	})
	r.harvestEnv([]string{"POSTGRES_USER=postgres", "POSTGRES_PASSWORD=postgres"})
	r.harvestEnv([]string{"RABBITMQ_DEFAULT_USER=admin", "RABBITMQ_DEFAULT_PASS=admin"})
	r.finalize()

	out := string(r.redact([]byte("citeck_seaweedfs_ns1 runs harbor.citeck.ru/x; key " + s3Key)))
	assert.Contains(t, out, "citeck_seaweedfs_ns1 runs harbor.citeck.ru/x",
		"the access key ID must not mask every identifier that contains it")
	assert.NotContains(t, out, s3Key, "the secret half of the pair is still masked")
	assert.False(t, isSecretEnvKey("AWS_ACCESS_KEY_ID"))
	assert.True(t, isSecretEnvKey("AWS_SECRET_ACCESS_KEY"))
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
