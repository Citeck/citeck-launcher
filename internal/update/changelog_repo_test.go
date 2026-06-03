package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoChangelogDir locates the repo-root changelog/ relative to this test file.
func repoChangelogDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "changelog")
}

// TestChangelogRepoConsistency enforces the hard contract: every release listed
// in index.json has a folder with ALL 8 locale files, and every release folder
// is listed in index.json.
func TestChangelogRepoConsistency(t *testing.T) {
	dir := repoChangelogDir()
	raw, err := os.ReadFile(filepath.Join(dir, "index.json")) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}
	var idx []indexEntry
	if jErr := json.Unmarshal(raw, &idx); jErr != nil {
		t.Fatalf("parse index.json: %v", jErr)
	}
	if len(idx) == 0 {
		t.Fatal("index.json is empty — the first 2b release must be listed")
	}

	indexed := map[string]bool{}
	for _, e := range idx {
		indexed[e.Version] = true
		if e.Date == "" {
			t.Errorf("index entry %s has empty date", e.Version)
		}
		for _, loc := range supportedLocales {
			p := filepath.Join(dir, e.Version, loc+".md")
			info, statErr := os.Stat(p)
			if statErr != nil {
				t.Errorf("missing required locale file: changelog/%s/%s.md", e.Version, loc)
				continue
			}
			if info.Size() == 0 {
				t.Errorf("empty locale file: changelog/%s/%s.md", e.Version, loc)
			}
		}
	}

	// Every version folder on disk must be in the index.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read changelog dir: %v", err)
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		if !indexed[ent.Name()] {
			t.Errorf("release folder changelog/%s/ is not listed in index.json", ent.Name())
		}
	}
}
