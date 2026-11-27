package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspaceWebappCompanions(t *testing.T) {
	raw := []byte(`
webapps:
  - id: ai
    aliases: [EcosAiApp]
    companions:
      litellm:
        environments:
          SPRING_AI_OPENAI_BASEURL: http://litellm:4000
          SPRING_AI_OPENAI_APIKEY: sk-${secret:litellm-master-key}
        dependsOn: [litellm]
  - id: rag
`)
	cfg := parseWorkspaceConfig(raw, "workspace-v2.yml", nil)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Webapps, 2)

	ai := cfg.Webapps[0]
	require.Contains(t, ai.Companions, "litellm")
	c := ai.Companions["litellm"]
	assert.Equal(t, "http://litellm:4000", c.Environments["SPRING_AI_OPENAI_BASEURL"])
	assert.Equal(t, "sk-${secret:litellm-master-key}", c.Environments["SPRING_AI_OPENAI_APIKEY"],
		"templates are resolved by the generator, not by the parser")
	assert.Equal(t, []string{"litellm"}, c.DependsOn)

	assert.Empty(t, cfg.Webapps[1].Companions, "a webapp without the key has no companions")
}

func TestParseWorkspaceLinkDescriptions(t *testing.T) {
	raw := []byte(`
links:
  - name: LiteLLM
    url: http://127.0.0.1:14000/ui
    description: "LLM gateway"
    descriptions:
      ru: "LLM-шлюз"
      en: "LLM gateway"
  - name: SeaweedFS
    url: http://127.0.0.1:23646
    description: "SeaweedFS"
`)
	cfg := parseWorkspaceConfig(raw, "workspace-v2.yml", nil)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Links, 2)
	assert.Equal(t, "LLM gateway", cfg.Links[0].Description)
	assert.Equal(t, "LLM-шлюз", cfg.Links[0].Descriptions.Resolve("ru"))
	assert.Empty(t, cfg.Links[1].Descriptions)
}

func TestParseWorkspaceMalformedCompanionsIsTotal(t *testing.T) {
	t.Run("non-map companions section drops only companions", func(t *testing.T) {
		raw := []byte(`
webapps:
  - id: ai
    aliases: [EcosAiApp]
    companions: [litellm]
  - id: rag
    companions:
      litellm:
        dependsOn: [litellm]
additionalApps:
  - name: litellm
    image: example/litellm:1.0.0
links:
  - name: LiteLLM
    url: http://127.0.0.1:14000/ui
`)
		cfg := parseWorkspaceConfig(raw, "workspace-v2.yml", nil)
		require.NotNil(t, cfg)
		require.Len(t, cfg.Webapps, 2)
		assert.Equal(t, "ai", cfg.Webapps[0].ID)
		assert.Equal(t, []string{"EcosAiApp"}, cfg.Webapps[0].Aliases)
		assert.Empty(t, cfg.Webapps[0].Companions)
		assert.Contains(t, cfg.Webapps[1].Companions, "litellm")
		require.Len(t, cfg.AdditionalApps, 1)
		require.Len(t, cfg.Links, 1)
	})

	t.Run("malformed companion block drops only that block and keeps sibling blocks", func(t *testing.T) {
		cases := map[string]string{
			"block is scalar":            "bad: oops",
			"environments is not a map":  "bad:\n        environments: [A=1]",
			"environment value is list":  "bad:\n        environments:\n          A: [1, 2]",
			"dependsOn is scalar":        "bad:\n        dependsOn: litellm",
			"dependsOn item is non-str":  "bad:\n        dependsOn: [litellm, {x: 1}]",
		}
		for name, badBlock := range cases {
			t.Run(name, func(t *testing.T) {
				raw := []byte(`
webapps:
  - id: ai
    companions:
      ` + badBlock + `
      good:
        environments:
          LLM_URL: http://good:4000
        dependsOn: [good]
additionalApps:
  - name: good
    image: example/good:1.0.0
links:
  - name: Good
    url: http://127.0.0.1:14000
`)
				cfg := parseWorkspaceConfig(raw, "workspace-v2.yml", nil)
				require.NotNil(t, cfg)
				require.Len(t, cfg.Webapps, 1)
				assert.NotContains(t, cfg.Webapps[0].Companions, "bad")
				require.Contains(t, cfg.Webapps[0].Companions, "good")
				assert.Equal(t, "http://good:4000", cfg.Webapps[0].Companions["good"].Environments["LLM_URL"])
				assert.Equal(t, []string{"good"}, cfg.Webapps[0].Companions["good"].DependsOn)
				require.Len(t, cfg.AdditionalApps, 1)
				require.Len(t, cfg.Links, 1)
			})
		}
	})
}
