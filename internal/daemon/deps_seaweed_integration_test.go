//go:build integration

package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (e *itEnv) swITRequest(ctx context.Context, t *testing.T, container, method, path string, body []byte) ([]byte, http.Header) {
	t.Helper()
	file := filepath.Join(e.base, "sw-client", "request.bin")
	require.NoError(t, os.WriteFile(file, body, 0o644))
	out, stderr, code, err := e.env.Exec(ctx, container, []string{"/citeck/seaweed-test/client", method, path})
	require.NoError(t, err)
	require.Zero(t, code, stderr)
	var response struct {
		Status  int
		Headers http.Header
		Body    []byte
	}
	require.NoError(t, json.Unmarshal([]byte(out), &response))
	// S3_BUCKET creates this bucket at startup; readiness may win the race
	// against the explicit bootstrap PUT. Only this ownership-confirming
	// response is idempotent success, never BucketAlreadyExists or other errors.
	if method == "PUT" && path == "/ecos-content" && response.Status == http.StatusConflict {
		var failure struct {
			Code string `xml:"Code"`
		}
		if xml.Unmarshal(response.Body, &failure) == nil && failure.Code == "BucketAlreadyOwnedByYou" {
			return response.Body, response.Headers
		}
	}
	require.Less(t, response.Status, 300, "S3 %s %s: %s", method, path, response.Body)
	return response.Body, response.Headers
}
func (e *itEnv) swStart(ctx context.Context, t *testing.T, image string, gen int, name string) string {
	t.Helper()
	id := deps.ID("seaweedfs")
	require.NoError(t, e.env.PullImage(ctx, image, func(float64) {}))
	def, err := e.env.GenerateDefFor(id, deps.DependencyState{Image: image, VolumeGen: gen})
	require.NoError(t, err)
	_, err = e.env.RunAppDef(ctx, def, deps.TempContainerOpts{Name: name, ExtraBinds: []string{filepath.Join(e.base, "sw-client") + ":/citeck/seaweed-test:ro"}})
	require.NoError(t, err)
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		_, _, code, err := e.env.Exec(ctx, name, []string{"sh", "-c", "wget -q -O /dev/null http://127.0.0.1:8333/status"})
		if err == nil && code == 0 {
			return name
		}
		time.Sleep(time.Second)
	}
	t.Fatal("SeaweedFS did not become ready")
	return ""
}
func TestIntegration_SeaweedCopyUpgradeAndRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), itTestBudget)
	defer cancel()
	id := deps.ID("seaweedfs")
	deps.SetExtraDependencies([]deps.Descriptor{deps.NewSeaweedDescriptor(id, "seaweedfs", "seaweedfs")})
	t.Cleanup(deps.ResetExtraDependencies)
	e := newITEnvFor(t, id, "chrislusf/seaweedfs:4.47")
	t.Setenv("CITECK_HOME", filepath.Join(e.base, "launcher-home"))
	helper := os.Getenv("CITECK_SEAWEED_TEST_CLIENT")
	require.NotEmpty(t, helper, "build tests/seaweed-client and set CITECK_SEAWEED_TEST_CLIENT")
	helperBytes, err := os.ReadFile(helper)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(e.base, "sw-client"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(e.base, "sw-client", "client"), helperBytes, 0o755))

	endpoint := e.swStart(ctx, t, "chrislusf/seaweedfs:4.47", 1, "sw-seed")
	e.swITRequest(ctx, t, endpoint, "PUT", "/ecos-content", nil)
	versioning := []byte(`<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Enabled</Status></VersioningConfiguration>`)
	e.swITRequest(ctx, t, endpoint, "PUT", "/ecos-content?versioning=", versioning)
	objects := map[string][]byte{"binary": bytes.Repeat([]byte{0, 255, 17, 42}, 256*1024), "папка/с пробелом.txt": []byte("контент UTF-8")}
	versions := map[string]string{}
	for key, body := range objects {
		_, headers := e.swITRequest(ctx, t, endpoint, "PUT", "/ecos-content/"+strings.ReplaceAll(url.PathEscape(key), "%2F", "/"), body)
		versions[key] = headers.Get("X-Amz-Version-Id")
	}
	old := []byte("previous version")
	_, headers := e.swITRequest(ctx, t, endpoint, "PUT", "/ecos-content/versioned", old)
	oldVersion := headers.Get("X-Amz-Version-Id")
	require.NotEmpty(t, oldVersion)
	e.swITRequest(ctx, t, endpoint, "PUT", "/ecos-content/versioned", []byte("current version"))
	require.NoError(t, e.env.StopRemove(ctx, "sw-seed"))
	before := e.volumeManifest(ctx, t, "seaweedfs2")
	plan, j, err := (migrate.SeaweedMigrator{ID: id}).Plan(ctx, e.env, migrate.Path{"chrislusf/seaweedfs:4.47", "chrislusf/seaweedfs:4.48"}, migrate.PlanOptions{})
	require.NoError(t, err)
	require.NoError(t, migrate.Run(ctx, e.rt, j, plan, nil))
	assert.Equal(t, before, e.volumeManifest(ctx, t, "seaweedfs2"), "migration changed original volume")
	pin := e.rt.DependencyStates()[id]
	assert.Equal(t, 2, pin.Gen())
	_, hasPrevious := pin.Previous()
	require.True(t, hasPrevious)
	endpoint = e.swStart(ctx, t, "chrislusf/seaweedfs:4.48", 2, "sw-check")
	for key, expected := range objects {
		data, _ := e.swITRequest(ctx, t, endpoint, "GET", "/ecos-content/"+strings.ReplaceAll(url.PathEscape(key), "%2F", "/")+"?versionId="+url.QueryEscape(versions[key]), nil)
		assert.Equal(t, sha256.Sum256(expected), sha256.Sum256(data))
	}
	data, _ := e.swITRequest(ctx, t, endpoint, "GET", "/ecos-content/versioned?versionId="+url.QueryEscape(oldVersion), nil)
	assert.Equal(t, old, data)
	require.NoError(t, e.env.StopRemove(ctx, "sw-check"))
	// A normal rollback returns to the retained original, never starts an old
	// binary on the upgraded data. The source still serves every original byte.
	frozen := e.volumeManifest(ctx, t, "seaweedfs3")
	rec := depsPost(e.mux, api.DependencyRollbackPath("seaweedfs"), "")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	e.d.bgWg.Wait()
	restored := e.rt.DependencyStates()[id]
	assert.Equal(t, "chrislusf/seaweedfs:4.47", restored.Image)
	assert.Equal(t, 1, restored.Gen())
	assert.Equal(t, frozen, e.volumeManifest(ctx, t, "seaweedfs3"), "rollback changed upgraded volume")

	endpoint = e.swStart(ctx, t, "chrislusf/seaweedfs:4.47", 1, "sw-old-check")
	for key, expected := range objects {
		data, _ := e.swITRequest(ctx, t, endpoint, "GET", "/ecos-content/"+strings.ReplaceAll(url.PathEscape(key), "%2F", "/"), nil)
		assert.Equal(t, sha256.Sum256(expected), sha256.Sum256(data))
	}
	require.NoError(t, e.env.StopRemove(ctx, "sw-old-check"))
	t.Log(fmt.Sprintf("PASS SeaweedFS 4.47 -> 4.48, generation %d, binary/UTF-8/versioned objects verified; original data retained", pin.Gen()))
}
