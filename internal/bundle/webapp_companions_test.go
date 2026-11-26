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
