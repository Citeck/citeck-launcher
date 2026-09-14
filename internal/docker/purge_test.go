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
		gotNs = append(gotNs, t.NS)
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

// A resource with no WORKSPACE label was created by a launcher in SERVER mode
// (Client.workspace is empty there — see client.go). A desktop profile's keep
// set can never contain such a pair, so the sweep read every server-mode stand
// on the host as an orphan of its own. Measured on one machine: 8 of the 21
// recorded purges were `ns=<something> ws=`, one of them a stand that was being
// used at the time. Same reasoning as the empty-namespace skip beside it — the
// pair does not belong to this profile and cannot be addressed safely.
func TestPairsWithoutAWorkspaceLabelAreNeverTargeted(t *testing.T) {
	keep := map[string]bool{OrphanKey("mine", "default"): true}
	labelSets := []map[string]string{
		{LabelNamespace: "default", LabelWorkspace: ""}, // a server-mode stand
		{LabelNamespace: "default"},                     // no workspace label at all
		{LabelNamespace: "gone", LabelWorkspace: "default"},
	}

	got := collectOrphanTargets(labelSets, keep)

	if len(got) != 1 || got[0].NS != "gone" {
		t.Fatalf("orphan targets = %+v, want only the desktop pair {gone default}", got)
	}
}
