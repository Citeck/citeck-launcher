package namespace

import (
	"strings"
	"testing"
)

func TestApplyPatchToNodePreservesOrderAndMergesNewKeys(t *testing.T) {
	out, err := applyDeltaToYAML([]byte("a: 1\nb: 2\nc: 3\n"), map[string]any{"a": "9"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `a: "9"`) && !strings.Contains(got, "a: 9") {
		t.Errorf("edit not applied:\n%s", got)
	}
	ai, bi, ci := strings.Index(got, "a:"), strings.Index(got, "b:"), strings.Index(got, "c:")
	if ai >= bi || bi >= ci {
		t.Errorf("order not preserved:\n%s", got)
	}
}

func TestGraftCommentsCopiesUserComments(t *testing.T) {
	template := []byte("image: app:2\nresources:\n  memLimit: 4g\n")
	user := []byte("# top comment\nimage: app:1\nresources:\n  memLimit: 4g # inline\n")
	out, err := applyDeltaToYAML(template, map[string]any{}, user)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "# top comment") {
		t.Errorf("head comment not grafted:\n%s", got)
	}
	if !strings.Contains(got, "# inline") {
		t.Errorf("inline comment not grafted:\n%s", got)
	}
	if !strings.Contains(got, "image: app:2") {
		t.Errorf("template data change must survive:\n%s", got)
	}
}

// Editing a file whose generated baseline is the flow-style empty mapping
// "{}" (the webapp cloud-config default) must NOT collapse the whole document
// onto one line — the populated root re-encodes as block YAML, not
// "{wqeqwe: [{dsds: sda}]}".
func TestApplyDeltaFromEmptyFlowMappingEmitsBlock(t *testing.T) {
	out, err := applyDeltaToYAML([]byte("{}\n"), map[string]any{
		"wqeqwe": []any{map[string]any{"dsds": "sda"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "{") || strings.Contains(got, "[") {
		t.Errorf("expected block YAML, got flow:\n%s", got)
	}
	if !strings.Contains(got, "wqeqwe:") || !strings.Contains(got, "- dsds: sda") {
		t.Errorf("content not preserved as block:\n%s", got)
	}
}

func TestApplyDeltaDeletesKey(t *testing.T) {
	out, err := applyDeltaToYAML([]byte("a: 1\nb: 2\n"), map[string]any{"b": nil}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "b:") {
		t.Errorf("key b should be deleted:\n%s", out)
	}
}
