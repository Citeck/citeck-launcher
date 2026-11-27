package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalizeLinkDescriptions(t *testing.T) {
	links := []api.LinkDto{
		{Name: "Mailpit", DescriptionKey: "links.mailpit.tooltip"},
		{Name: "Plain", Description: "Only one text", Custom: true},
		{Name: "LiteLLM", Description: "LLM gateway", Custom: true,
			Descriptions: map[string]string{"ru": "LLM-шлюз", "en": "LLM gateway"}},
		{Name: "RuOnlyWithFallback", Description: "LLM gateway", Custom: true,
			Descriptions: map[string]string{"ru": "LLM-шлюз"}},
		{Name: "NoPlainFallback", Custom: true,
			Descriptions: map[string]string{"ru": "LLM-шлюз", "de": "LLM-Gateway"}},
		{Name: "PlainKeyOverride", Description: "Old fallback", Custom: true,
			Descriptions: map[string]string{"": "Single-language override"}},
	}

	ru := append([]api.LinkDto(nil), links...)
	localizeLinkDescriptions(ru, "ru-RU")
	assert.Equal(t, "LLM-шлюз", ru[2].Description, "the reader's language")
	assert.Equal(t, "Only one text", ru[1].Description, "a link with one text keeps it")
	assert.Empty(t, ru[0].Description, "built-in links are translated by the web UI from their key")
	assert.Equal(t, "LLM-шлюз", ru[3].Description, "ru-RU matches ru translation")
	assert.Equal(t, "LLM-шлюз", ru[4].Description)
	assert.Equal(t, "Single-language override", ru[5].Description, "plainTextKey takes priority")

	en := append([]api.LinkDto(nil), links...)
	localizeLinkDescriptions(en, "en")
	assert.Equal(t, "LLM gateway", en[3].Description, "plain description is preserved when descriptions omits en")
	assert.Equal(t, "LLM-Gateway", en[4].Description, "empty plain description falls back to first alphabetical key")

	de := append([]api.LinkDto(nil), links...)
	localizeLinkDescriptions(de, "de")
	assert.Equal(t, "LLM gateway", de[2].Description, "a language the author did not write falls back to English")
	assert.Equal(t, "LLM gateway", de[3].Description, "plain description beats an unrelated language when en is absent")
	assert.Equal(t, "LLM-Gateway", de[4].Description)
}

func TestHandleGetNamespaceLocalizesLinkDescriptionsAndOmitsRawMap(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns", Name: "Demo"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.SetCustomLinks([]bundle.WorkspaceLink{
		{
			Name:         "LiteLLM",
			URL:          "http://127.0.0.1:14000/ui",
			Description:  "LLM gateway",
			Descriptions: bundle.LocalizedText{"ru": "LLM-шлюз"},
		},
	})

	d := &Daemon{
		daemonCfg: config.DaemonConfig{Locale: "en"},
		activeNs: &activeNamespace{
			runtime:  rt,
			nsConfig: &namespace.Config{ID: "ns", Name: "Demo"},
		},
	}

	requestLink := func(localeHeader string) (api.LinkDto, []byte) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/namespace", http.NoBody)
		if localeHeader != "" {
			req.Header.Set(LocaleHeader, localeHeader)
		}
		rec := httptest.NewRecorder()
		d.handleGetNamespace(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var dto api.NamespaceDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		for _, l := range dto.Links {
			if l.Name == "LiteLLM" {
				return l, rec.Body.Bytes()
			}
		}
		t.Fatalf("LiteLLM link not found in response")
		return api.LinkDto{}, nil
	}

	ruLink, rawRu := requestLink("ru")
	assert.Equal(t, "LLM-шлюз", ruLink.Description)
	assert.NotContains(t, string(rawRu), `"descriptions"`, "raw per-language map must not be serialized in JSON")

	deLink, rawDe := requestLink("de")
	assert.Equal(t, "LLM gateway", deLink.Description, "missing de/en falls back to plain description")
	assert.NotContains(t, string(rawDe), `"descriptions"`)

	enLink, _ := requestLink("en")
	assert.Equal(t, "LLM gateway", enLink.Description)
}
