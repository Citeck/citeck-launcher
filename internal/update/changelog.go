package update

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Latest identifies the newest release: Tag is the git ref (e.g. "v2.6.0") used
// for raw fetches; Version is the bare semver (e.g. "2.6.0") used for comparison.
type Latest struct {
	Tag     string `json:"tag"`
	Version string `json:"version"`
}

// ReleaseNote is one release's changelog entry shown in the UI.
type ReleaseNote struct {
	Version  string `json:"version"`
	Date     string `json:"date"`
	Markdown string `json:"markdown"`
}

// indexEntry is one row of changelog/index.json.
type indexEntry struct {
	Version string `json:"version"`
	Date    string `json:"date"`
}

// supportedLocales is the set of UI locales; mirrors web/src/locales and
// internal/i18n/locales. en is the fallback and is always required per release.
//
//nolint:unused // consumed by changelog_repo_test.go (Task 16); remove this directive there.
var supportedLocales = []string{"en", "ru", "zh", "es", "de", "fr", "pt", "ja"}

// changelog fetches changelog/index.json at the latest tag, filters releases in
// (current, latest], and returns their notes newest-first. Per release it fetches
// <ver>/<locale>.md, falling back to <ver>/en.md when the locale file is absent.
func changelog(ctx context.Context, c *client, current string, latest Latest, locale string) ([]ReleaseNote, error) {
	raw, err := c.fetchRaw(ctx, latest.Tag, "changelog/index.json")
	if err != nil {
		return nil, fmt.Errorf("fetch changelog index: %w", err)
	}
	var idx []indexEntry
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("parse changelog index: %w", err)
	}

	// Keep versions in (current, latest]: newer than current AND not newer than latest.
	inRange := idx[:0:0]
	for _, e := range idx {
		if Greater(e.Version, current) && !Greater(e.Version, latest.Version) {
			inRange = append(inRange, e)
		}
	}
	// Newest first.
	sort.Slice(inRange, func(i, j int) bool { return Greater(inRange[i].Version, inRange[j].Version) })

	notes := make([]ReleaseNote, 0, len(inRange))
	for _, e := range inRange {
		md := fetchLocaleMarkdown(ctx, c, latest.Tag, e.Version, locale)
		notes = append(notes, ReleaseNote{Version: e.Version, Date: e.Date, Markdown: md})
	}
	return notes, nil
}

// fetchLocaleMarkdown returns the localized release note, falling back to en and
// then to an empty string (the UI still shows version + date).
func fetchLocaleMarkdown(ctx context.Context, c *client, tag, version, locale string) string {
	if locale != "" && locale != "en" {
		if b, err := c.fetchRaw(ctx, tag, fmt.Sprintf("changelog/%s/%s.md", version, locale)); err == nil {
			return string(b)
		}
	}
	if b, err := c.fetchRaw(ctx, tag, fmt.Sprintf("changelog/%s/en.md", version)); err == nil {
		return string(b)
	}
	return ""
}
