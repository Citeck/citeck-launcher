package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// setupRegistryAuthHome creates a CITECK_HOME tempdir containing a minimal
// workspace-v1.yml and returns the path. The workspace declares a single
// auth-required image registry matched to one bundle repo whose bundle YAML
// references that registry.
//
// To actually resolve the bundle (and thus scope auth to "used" repos), a
// bundle YAML file is also written. The caller passes its ref as
// "<repoID>:<key>".
func setupRegistryAuthHome(t *testing.T, withAuthRepo, withUnusedAuthRepo bool) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)

	// Reset the data dir used by resolver.NewResolver(config.DataDir()).
	dataDir := filepath.Join(home, "data")
	wsRepoDir := filepath.Join(dataDir, "bundles", "workspace")
	if err := os.MkdirAll(wsRepoDir, 0o755); err != nil {
		t.Fatalf("mkdir wsRepoDir: %v", err)
	}

	// Build workspace YAML.
	var repos strings.Builder
	if withAuthRepo {
		repos.WriteString("  - id: private\n    url: registry.example.com\n    authType: BASIC\n")
	}
	if withUnusedAuthRepo {
		repos.WriteString("  - id: unused\n    url: unused.example.com\n    authType: BASIC\n")
	}

	workspaceYAML := "imageRepos:\n" + repos.String() + `bundleRepos:
  - id: r1
    name: Repo 1
`
	if err := os.WriteFile(filepath.Join(wsRepoDir, "workspace-v1.yml"), []byte(workspaceYAML), 0o644); err != nil {
		t.Fatalf("write workspace-v1.yml: %v", err)
	}

	// Bundle YAML for ref r1:v1 — references the private registry so
	// bundleImageRepoIDs maps it to "private". Bundle repo r1 has no URL,
	// so bundles are read from the workspace dir (shouldUseLocalBundles).
	// Bundle top-level entries are app names; image.repository uses a
	// <imageRepoID>/<path> prefix that resolves to the registry URL.
	bundleYAML := `App1:
  image:
    repository: private/app1
    tag: "1.0"
`
	if err := os.WriteFile(filepath.Join(wsRepoDir, "2026.1.yml"), []byte(bundleYAML), 0o644); err != nil {
		t.Fatalf("write bundle yaml: %v", err)
	}

	// Conf dir for the SecretService file store.
	if err := os.MkdirAll(filepath.Join(home, "conf"), 0o755); err != nil {
		t.Fatalf("mkdir confdir: %v", err)
	}
}

func saveTestRegistryCred(t *testing.T, value string) {
	t.Helper()
	store, err := storage.NewFileStore(config.ConfDir())
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	defer store.Close()
	if err := store.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{
			ID:    "registry-private",
			Name:  "test",
			Type:  storage.SecretRegistryAuth,
			Scope: "test",
		},
		Value: value,
	}); err != nil {
		t.Fatalf("save secret: %v", err)
	}
}

// TestCheckRegistryAuthForBundle_NoAuthRepos: when the workspace has no
// auth-required registries, the check is a no-op.
func TestCheckRegistryAuthForBundle_NoAuthRepos(t *testing.T) {
	setupRegistryAuthHome(t, false, false)

	err := checkRegistryAuthForBundle(bundle.Ref{Repo: "r1", Key: "2026.1"})
	if err != nil {
		t.Errorf("no-op expected, got error: %v", err)
	}
}

// TestCheckRegistryAuthForBundle_NonTTYMissingCreds: non-TTY + missing creds
// for the target's registry must return ExitConfigError.
func TestCheckRegistryAuthForBundle_NonTTYMissingCreds(t *testing.T) {
	setupRegistryAuthHome(t, true, false)

	// Simulate non-TTY (output.IsTTY() reads os.Stdout fd — we cannot easily
	// override it, but in `go test` stdout is typically NOT a TTY, so
	// output.IsTTY() returns false by default). Verified via the test run.
	err := checkRegistryAuthForBundle(bundle.Ref{Repo: "r1", Key: "2026.1"})
	if err == nil {
		t.Fatal("expected ExitConfigError for missing creds in non-TTY, got nil")
	}
	var ece ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("expected ExitCodeError, got %T: %v", err, err)
	}
	if ece.Code != ExitConfigError {
		t.Errorf("expected exit code %d, got %d", ExitConfigError, ece.Code)
	}
	if !strings.Contains(err.Error(), "registry.example.com") {
		t.Errorf("error should name the registry host, got: %v", err)
	}
}

// TestCheckRegistryAuthForBundle_ValidCredsProbed: saved creds with a login
// probe that succeeds should be accepted — no error, no prompt.
func TestCheckRegistryAuthForBundle_ValidCredsProbed(t *testing.T) {
	setupRegistryAuthHome(t, true, false)
	saveTestRegistryCred(t, "alice:password123")

	origLogin := dockerRegistryLoginFunc
	t.Cleanup(func() { dockerRegistryLoginFunc = origLogin })
	calls := 0
	dockerRegistryLoginFunc = func(host, user, pass string) error {
		calls++
		if host != "registry.example.com" {
			t.Errorf("unexpected host %q", host)
		}
		if user != "alice" || pass != "password123" {
			t.Errorf("unexpected creds user=%q pass=%q", user, pass)
		}
		return nil
	}

	err := checkRegistryAuthForBundle(bundle.Ref{Repo: "r1", Key: "2026.1"})
	if err != nil {
		t.Errorf("expected nil for valid saved creds, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 login probe, got %d", calls)
	}
}

// TestCheckRegistryAuthForBundle_InvalidSavedCredsNonTTY: saved creds whose
// probe FAILS must be treated as missing — in non-TTY, this surfaces as
// ExitConfigError.
func TestCheckRegistryAuthForBundle_InvalidSavedCredsNonTTY(t *testing.T) {
	setupRegistryAuthHome(t, true, false)
	saveTestRegistryCred(t, "alice:stale")

	origLogin := dockerRegistryLoginFunc
	t.Cleanup(func() { dockerRegistryLoginFunc = origLogin })
	dockerRegistryLoginFunc = func(host, user, pass string) error {
		return fmt.Errorf("401 unauthorized")
	}

	err := checkRegistryAuthForBundle(bundle.Ref{Repo: "r1", Key: "2026.1"})
	if err == nil {
		t.Fatal("expected error when saved creds fail login probe, got nil")
	}
	var ece ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("expected ExitCodeError, got %T: %v", err, err)
	}
	if ece.Code != ExitConfigError {
		t.Errorf("expected exit code %d, got %d", ExitConfigError, ece.Code)
	}
}

// TestCheckRegistryAuthForBundle_MalformedCredNonTTY: a saved secret that
// does not parse as "user:pass" is treated as missing.
func TestCheckRegistryAuthForBundle_MalformedCredNonTTY(t *testing.T) {
	setupRegistryAuthHome(t, true, false)
	saveTestRegistryCred(t, "no-colon-here")

	origLogin := dockerRegistryLoginFunc
	t.Cleanup(func() { dockerRegistryLoginFunc = origLogin })
	dockerRegistryLoginFunc = func(host, user, pass string) error {
		t.Errorf("login probe should not be called for malformed cred")
		return nil
	}

	err := checkRegistryAuthForBundle(bundle.Ref{Repo: "r1", Key: "2026.1"})
	if err == nil {
		t.Fatal("expected error for malformed cred in non-TTY, got nil")
	}
	var ece ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("expected ExitCodeError, got %T: %v", err, err)
	}
}

// TestCheckRegistryAuthForBundle_ScopedToUsedRepos: an auth repo that is NOT
// used by the target bundle must NOT be probed or flagged.
func TestCheckRegistryAuthForBundle_ScopedToUsedRepos(t *testing.T) {
	setupRegistryAuthHome(t, true, true) // includes "unused" auth repo
	saveTestRegistryCred(t, "alice:ok")
	// No creds for "unused" — but it should be ignored because the bundle
	// doesn't reference unused.example.com.

	origLogin := dockerRegistryLoginFunc
	t.Cleanup(func() { dockerRegistryLoginFunc = origLogin })
	dockerRegistryLoginFunc = func(host, user, pass string) error {
		if host != "registry.example.com" {
			t.Errorf("probe called for out-of-scope host %q", host)
		}
		return nil
	}

	err := checkRegistryAuthForBundle(bundle.Ref{Repo: "r1", Key: "2026.1"})
	if err != nil {
		t.Errorf("unused auth repo must not trigger failure, got: %v", err)
	}
}
