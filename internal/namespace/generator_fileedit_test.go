package namespace

import (
	"strings"
	"testing"
)

func TestApplyFileEditsIntoFiles(t *testing.T) {
	files := map[string][]byte{"eapps/app/application.yml": []byte("a: 1\nb: 2\n")}
	e, _ := MakeFileEdit("application.yml", []byte("a: 1\nb: 2\n"), []byte("a: 9\nb: 2\n"))
	edits := map[string]FileEdit{"eapps/app/application.yml": e}
	disk := map[string][]byte{"eapps/app/application.yml": []byte("a: 9\nb: 2\n")}

	applyFileEditsToFiles(files, edits, disk)
	if !strings.Contains(string(files["eapps/app/application.yml"]), "9") {
		t.Errorf("edit not merged into ctx.Files:\n%s", files["eapps/app/application.yml"])
	}
}

func TestApplyFileEditsSkipsMissingTemplate(t *testing.T) {
	files := map[string][]byte{}
	e, _ := MakeFileEdit("x.sh", []byte("a\n"), []byte("b\n"))
	edits := map[string]FileEdit{"eapps/x.sh": e}
	applyFileEditsToFiles(files, edits, nil) // must not panic on absent template
	if len(files) != 0 {
		t.Errorf("no template → nothing written, got %v", files)
	}
}
