package namespace

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func genWithRabbitPatch(t *testing.T, patch json.RawMessage) *GenResp {
	t.Helper()
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	opts := GenerateOpts{}
	if patch != nil {
		opts.EditedAppPatches = map[string]json.RawMessage{appdef.AppRabbitmq: patch}
	}
	resp, err := Generate(cfg, &bundle.Def{}, &bundle.WorkspaceConfig{}, SystemSecrets{
		JWT: "j", OIDC: "o", AdminPassword: "a", CiteckSA: "s",
	}, opts)
	require.NoError(t, err)
	return resp
}

// findRabbitmq returns the rabbitmq ApplicationDef from a def slice. Every
// test in this file patches/inspects rabbitmq specifically, so this is not
// parameterized by name (a generic `name` param would always receive the
// same argument — unparam).
func findRabbitmq(apps []appdef.ApplicationDef) *appdef.ApplicationDef {
	for i := range apps {
		if apps[i].Name == appdef.AppRabbitmq {
			return &apps[i]
		}
	}
	return nil
}

func TestGenerate_EffectiveAndBaselineSplit(t *testing.T) {
	patch := json.RawMessage(`{"resources":{"limits":{"memory":"2g"}}}`)
	resp := genWithRabbitPatch(t, patch)

	base := findRabbitmq(resp.BaselineApplications)
	eff := findRabbitmq(resp.Applications)
	require.NotNil(t, base)
	require.NotNil(t, eff)

	assert.Equal(t, "1g", base.Resources.Limits.Memory, "baseline stays the const, patch-free")
	assert.Equal(t, "2g", eff.Resources.Limits.Memory, "effective carries the patch")

	// Conf derived from the EFFECTIVE memory (2 GiB = 2147483648).
	conf := resp.Files["rabbitmq/citeck-memory.conf"]
	assert.True(t, bytes.Contains(conf, []byte("total_memory_available_override_value = 2147483648")),
		"conf override must follow the effective memory, got %s", conf)
}

func TestGenerate_NoPatchIsUnchanged(t *testing.T) {
	resp := genWithRabbitPatch(t, nil)
	base := findRabbitmq(resp.BaselineApplications)
	eff := findRabbitmq(resp.Applications)
	assert.Equal(t, "1g", base.Resources.Limits.Memory)
	assert.Equal(t, "1g", eff.Resources.Limits.Memory)
	// Conf from the const (1 GiB = 1073741824).
	assert.Equal(t, rabbitmqMemoryConf("1g"), string(resp.Files["rabbitmq/citeck-memory.conf"]))
}

func TestGenerate_DeletedResourcesFallsBackToConst(t *testing.T) {
	resp := genWithRabbitPatch(t, json.RawMessage(`{"resources":null}`))
	eff := findRabbitmq(resp.Applications)
	assert.Nil(t, eff.Resources, "effective def has resources removed")
	// Conf falls back to the const (never empty/zero).
	assert.Equal(t, rabbitmqMemoryConf("1g"), string(resp.Files["rabbitmq/citeck-memory.conf"]))
}

// TestGenerate_FileEditSurvivesAppPatch pins that a user's file-edit delta on
// the rabbitmq conf lands ON TOP of the derived (effective-memory) template,
// not the other way around — deriveRabbitMemoryConf must run before the
// baselineFiles snapshot and file-edit merge (see the Generate tail ordering).
func TestGenerate_FileEditSurvivesAppPatch(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	patch := json.RawMessage(`{"resources":{"limits":{"memory":"2g"}}}`)

	// Build a file edit as a textual delta over the template that
	// deriveRabbitMemoryConf produces for the EFFECTIVE (2g) memory — this is
	// exactly the ctx.Files content in place when applyFileEditsToFiles runs,
	// given the Generate tail ordering under test.
	template := []byte(rabbitmqMemoryConf("2g"))
	edited := append(append([]byte{}, template...), []byte("# user watermark\n")...)
	fileEdit, err := MakeFileEdit("citeck-memory.conf", template, edited)
	require.NoError(t, err)

	opts := GenerateOpts{
		EditedAppPatches: map[string]json.RawMessage{appdef.AppRabbitmq: patch},
		EditedFileEdits: map[string]FileEdit{
			"rabbitmq/citeck-memory.conf": fileEdit,
		},
	}
	resp, err := Generate(cfg, &bundle.Def{}, &bundle.WorkspaceConfig{}, SystemSecrets{
		JWT: "j", OIDC: "o", AdminPassword: "a", CiteckSA: "s",
	}, opts)
	require.NoError(t, err)

	eff := findRabbitmq(resp.Applications)
	require.NotNil(t, eff)
	assert.Equal(t, "2g", eff.Resources.Limits.Memory, "app patch still applied")

	// The baseline file snapshot (pre-file-edit) must be the derived conf, so
	// the editor's diff-gutter is computed against the effective-memory template.
	baseline := resp.BaselineFiles["rabbitmq/citeck-memory.conf"]
	assert.True(t, bytes.Contains(baseline, []byte("total_memory_available_override_value = 2147483648")),
		"baseline file snapshot must reflect the effective memory, got %s", baseline)

	// The final file (post-file-edit merge) must carry the user's watermark.
	final := resp.Files["rabbitmq/citeck-memory.conf"]
	assert.True(t, bytes.Contains(final, []byte("user watermark")),
		"file edit must be merged on top of the derived template, got %s", final)
}
