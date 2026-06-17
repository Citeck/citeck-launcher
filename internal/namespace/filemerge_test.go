package namespace

import (
	"bytes"
	"strings"
	"testing"
)

func TestStructuralYamlMergeFlowsTemplateAndKeepsEdit(t *testing.T) {
	template := "a: 1\nb: 2\n"
	edited := "a: 9\nb: 2\n"
	edit, err := MakeFileEdit("application.yml", []byte(template), []byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	if edit.Format != "structural" {
		t.Fatalf("want structural, got %q", edit.Format)
	}
	newTemplate := "a: 1\nb: 2\nc: 3\n"
	got, err := ApplyFileEdit("application.yml", edit, []byte(newTemplate), []byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "9") || !strings.Contains(string(got), "a:") {
		t.Errorf("user edit lost:\n%s", got)
	}
	if !strings.Contains(string(got), "c: 3") {
		t.Errorf("new template key not merged:\n%s", got)
	}
}

func TestStructuralYamlPreservesUserComments(t *testing.T) {
	template := "a: 1\nb: 2\n"
	edited := "# my comment\na: 9\nb: 2\n"
	edit, _ := MakeFileEdit("conf.yaml", []byte(template), []byte(edited))
	got, err := ApplyFileEdit("conf.yaml", edit, []byte("a: 1\nb: 2\n"), []byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "# my comment") {
		t.Errorf("user comment lost:\n%s", got)
	}
}

func TestTextualMergeCleanApply(t *testing.T) {
	template := "line1\nline2\nline3\n"
	edited := "line1\nEDITED\nline3\n"
	edit, err := MakeFileEdit("entrypoint.sh", []byte(template), []byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	if edit.Format != "textual" {
		t.Fatalf("want textual, got %q", edit.Format)
	}
	newTemplate := "line1\nline2\nline3CHANGED\n"
	got, err := ApplyFileEdit("entrypoint.sh", edit, []byte(newTemplate), []byte(edited))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "EDITED") {
		t.Errorf("user edit lost:\n%s", got)
	}
}

func TestTextualMergeConflictFallsBackToCurrent(t *testing.T) {
	template := "alpha\nbeta\ngamma\n"
	edited := "alpha\nBETA-EDIT\ngamma\n"
	edit, _ := MakeFileEdit("run.sh", []byte(template), []byte(edited))
	newTemplate := "totally\ndifferent\nfile\ncontents\n"
	current := []byte(edited)
	got, err := ApplyFileEdit("run.sh", edit, []byte(newTemplate), current)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, current) {
		t.Errorf("conflict must fall back to current on-disk content, got:\n%s", got)
	}
}

func TestIsStructuralFile(t *testing.T) {
	for _, f := range []string{"a.yml", "a.yaml", "a.json", "dir/x.YML"} {
		if !isStructuralFile(f) {
			t.Errorf("%q should be structural", f)
		}
	}
	for _, f := range []string{"run.sh", "Dockerfile", "x.properties", "a.conf"} {
		if isStructuralFile(f) {
			t.Errorf("%q should be textual", f)
		}
	}
}
