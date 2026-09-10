package i18n_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/msg"
)

// The whole reason this type exists beside the package-level T: the daemon
// answers a desktop UI in one language and a CLI in another at the same time,
// so two translators must not be able to see each other's locale.
func TestTwoTranslatorsDoNotShareALocale(t *testing.T) {
	ru := i18n.NewTranslator("ru")
	de := i18n.NewTranslator("de")
	key := "deps.col.dependency"
	assert.NotEqual(t, ru.T(key), de.T(key), "each translator renders its own locale")
	assert.Equal(t, "ru", ru.Locale())
	assert.Equal(t, "de", de.Locale())
}

// A locale arrives from a request header, i.e. from outside. Anything
// unrecognizable must degrade to English rather than fail: the sentence is the
// point, the language is a preference.
func TestAnUnknownLocaleFallsBackToEnglishInsteadOfFailing(t *testing.T) {
	en := i18n.NewTranslator("en")
	for _, bad := range []string{"", "klingon", "zz", "  ", "e"} {
		tr := i18n.NewTranslator(bad)
		assert.Equal(t, "en", tr.Locale(), "locale %q", bad)
		assert.Equal(t, en.T("deps.col.status"), tr.T("deps.col.status"), "locale %q", bad)
	}
}

// Browsers and CLIs send region tags and mixed case; the launcher ships plain
// two-letter locales.
func TestRegionAndCaseAreNormalized(t *testing.T) {
	for _, in := range []string{"ru", "RU", "ru-RU", "ru_RU", " ru-ru "} {
		assert.Equal(t, "ru", i18n.NormalizeLocale(in), "input %q", in)
	}
	assert.Equal(t, "en", i18n.NormalizeLocale("en-GB"))
}

// A key the locale does not carry must fall back to English per KEY, not per
// FILE: a locale that is one key behind must not lose the other 452.
// A key nobody carries renders as ITSELF, so a missing key looks missing
// instead of looking like a sentence with nothing to say. (The per-key English
// fallback in NewTranslator is not exercised here and cannot be: key parity is
// enforced, so no supported locale is missing a key that en has. See its doc.)
func TestAKeyNobodyHasRendersAsItself(t *testing.T) {
	ru := i18n.NewTranslator("ru")
	assert.NotEqual(t, "deps.col.status", ru.T("deps.col.status"), "a present key is translated")
	assert.Equal(t, "no.such.key.anywhere", ru.T("no.such.key.anywhere"))
}

func TestRenderInterpolatesTheMessageArgs(t *testing.T) {
	tr := i18n.NewTranslator("en")
	// deps.statusDetail is "{id}: {detail}" in every locale — a pure format.
	got := tr.Render(msg.New("deps.statusDetail", "id", "rabbitmq", "detail", "held back"))
	assert.Equal(t, "rabbitmq: held back", got)
}

// The zero Message renders empty — the property callers rely on when they ask
// for "the sentence, if there is one".
func TestTheZeroMessageRendersEmpty(t *testing.T) {
	assert.Empty(t, i18n.NewTranslator("en").Render(msg.Message{}))
	assert.True(t, msg.Message{}.Empty())
	assert.False(t, msg.New("k").Empty())
}

// Problems and Warnings are JSON arrays that must never marshal as null.
func TestRenderAllNeverReturnsNil(t *testing.T) {
	out := i18n.NewTranslator("en").RenderAll(nil)
	require.NotNil(t, out)
	assert.Empty(t, out)
}

// An odd Args is a programming error; the sentence is worth more than the
// daemon, so it must not panic.
func TestAnOddArgListDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		i18n.NewTranslator("en").Render(msg.New("deps.statusDetail", "id"))
	})
}

// The cached maps are shared by every translator, so they are read from many
// goroutines at once. -race is the point of this test.
func TestTranslatorsAreSafeUnderConcurrentUse(t *testing.T) {
	var wg sync.WaitGroup
	for _, loc := range []string{"en", "ru", "de", "fr", "es", "pt", "ja", "zh"} {
		for range 8 {
			wg.Add(1)
			go func(l string) {
				defer wg.Done()
				tr := i18n.NewTranslator(l)
				_ = tr.T("deps.col.dependency")
				_ = tr.Render(msg.New("deps.statusDetail", "id", "x", "detail", "y"))
			}(loc)
		}
	}
	wg.Wait()
}
