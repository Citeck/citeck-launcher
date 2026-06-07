package docker

import (
	"sort"
	"testing"
)

func TestOrphanKeyCaseInsensitiveWorkspace(t *testing.T) {
	// Namespace is matched exactly; workspace is folded to lower case so a
	// label written as "Default" still matches a stored "default".
	if OrphanKey("abc", "Default") != OrphanKey("abc", "default") {
		t.Fatalf("workspace should be case-folded in the key")
	}
	if OrphanKey("abc", "x") == OrphanKey("abd", "x") {
		t.Fatalf("distinct namespaces must produce distinct keys")
	}
}

func TestCollectOrphanTargets(t *testing.T) {
	keep := map[string]bool{
		OrphanKey("txzupma", "default"): true, // active / stored namespace
	}
	labelSets := []map[string]string{
		// active ns — many resources, all kept
		{LabelNamespace: "txzupma", LabelWorkspace: "default"},
		{LabelNamespace: "txzupma", LabelWorkspace: "default"},
		// orphan ns vazzgla — several resources collapse to one target
		{LabelNamespace: "vazzgla", LabelWorkspace: "default"},
		{LabelNamespace: "vazzgla", LabelWorkspace: "default"},
		{LabelNamespace: "vazzgla", LabelWorkspace: "Default"}, // case-variant ws, same target
		// orphan ns a2uhq4a
		{LabelNamespace: "a2uhq4a", LabelWorkspace: "default"},
		// degenerate: empty namespace label — skipped (cannot address safely)
		{LabelNamespace: "", LabelWorkspace: "default"},
		{LabelWorkspace: "default"}, // missing ns label entirely
	}

	got := collectOrphanTargets(labelSets, keep)

	gotNs := make([]string, 0, len(got))
	for _, t := range got {
		gotNs = append(gotNs, t.ns)
	}
	sort.Strings(gotNs)
	want := []string{"a2uhq4a", "vazzgla"}
	if len(gotNs) != len(want) {
		t.Fatalf("orphan targets = %v, want %v", gotNs, want)
	}
	for i := range want {
		if gotNs[i] != want[i] {
			t.Fatalf("orphan targets = %v, want %v", gotNs, want)
		}
	}
}

func TestCollectOrphanTargetsAllKept(t *testing.T) {
	keep := map[string]bool{
		OrphanKey("a", "default"): true,
		OrphanKey("b", "default"): true,
	}
	labelSets := []map[string]string{
		{LabelNamespace: "a", LabelWorkspace: "default"},
		{LabelNamespace: "b", LabelWorkspace: "default"},
	}
	if got := collectOrphanTargets(labelSets, keep); len(got) != 0 {
		t.Fatalf("expected no orphans when every pair is kept, got %v", got)
	}
}
