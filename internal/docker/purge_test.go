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

// The workspace half of the membership rule is FOLDED and the namespace half is
// not, and that asymmetry is load-bearing: orphanKey lower-cases the workspace
// to build the keep set, so a pair kept there must be recognized here, or the
// sweep removes containers the keep set was protecting. Mutating either half of
// this function used to change nothing — both copies of the rule were reached
// only through the Docker client, which no test in this package has.
func TestLabelsMatchPairFoldsTheWorkspaceButNotTheNamespace(t *testing.T) {
	labels := map[string]string{LabelNamespace: "txzupma", LabelWorkspace: "Default"}

	if !labelsMatchPair(labels, "txzupma", "default") {
		t.Fatalf("a workspace label written as %q must match a stored %q", "Default", "default")
	}
	if !labelsMatchPair(labels, "txzupma", "DEFAULT") {
		t.Fatalf("folding must work from either side")
	}
	if labelsMatchPair(labels, "TXZUPMA", "default") {
		t.Fatalf("the namespace id is matched exactly — labels carry the raw id")
	}
	if labelsMatchPair(labels, "txzupma", "other") {
		t.Fatalf("a different workspace is a different pair")
	}
	if labelsMatchPair(map[string]string{LabelNamespace: "txzupma"}, "txzupma", "default") {
		t.Fatalf("a missing workspace label is not a match for a named workspace")
	}
}
