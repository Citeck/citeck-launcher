package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestLocalizeLinkDescriptions(t *testing.T) {
	links := []api.LinkDto{
		{Name: "Mailpit", DescriptionKey: "links.mailpit.tooltip"},
		{Name: "Plain", Description: "Only one text", Custom: true},
		{Name: "LiteLLM", Description: "LLM gateway", Custom: true,
			Descriptions: map[string]string{"ru": "LLM-шлюз", "en": "LLM gateway"}},
	}

	ru := append([]api.LinkDto(nil), links...)
	localizeLinkDescriptions(ru, "ru-RU")
	assert.Equal(t, "LLM-шлюз", ru[2].Description, "the reader's language")
	assert.Equal(t, "Only one text", ru[1].Description, "a link with one text keeps it")
	assert.Empty(t, ru[0].Description, "built-in links are translated by the web UI from their key")

	de := append([]api.LinkDto(nil), links...)
	localizeLinkDescriptions(de, "de")
	assert.Equal(t, "LLM gateway", de[2].Description, "a language the author did not write falls back to English")
}
