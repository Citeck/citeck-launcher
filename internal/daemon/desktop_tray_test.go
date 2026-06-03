package daemon

import "testing"

func TestBuildTrayMenuHasCoreItems(t *testing.T) {
	menu := buildTrayMenu()
	ids := map[string]TrayAction{}
	for _, it := range menu.Items {
		if it.Label == "" {
			t.Fatalf("item %s has empty label", it.ID)
		}
		ids[it.ID] = it.Action
	}
	for _, want := range []string{"open", "dump", "open-dir", "devtools", "exit"} {
		if _, ok := ids[want]; !ok {
			t.Fatalf("missing tray item %q", want)
		}
	}
	if ids["open"].Kind != "verb" || ids["open"].Verb != "window.focus" {
		t.Fatalf("open action = %+v", ids["open"])
	}
	if ids["exit"].Verb != "app.quit" {
		t.Fatalf("exit action = %+v", ids["exit"])
	}
}
