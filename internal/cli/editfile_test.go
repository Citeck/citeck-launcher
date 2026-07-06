package cli

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
)

type fakeFileClient struct {
	getDTO     *api.AppFileContentDto
	putCalls   [][]byte
	putErrs    []error // consumed per PutAppFile call
	resetPath  string
	reloadHits int
	listDTO    []api.AppFileDto
}

func (f *fakeFileClient) GetAppFile(string, string) (*api.AppFileContentDto, error) {
	return f.getDTO, nil
}
func (f *fakeFileClient) PutAppFile(_, _ string, body []byte) (*api.ActionResultDto, error) {
	f.putCalls = append(f.putCalls, body)
	if len(f.putErrs) > 0 {
		err := f.putErrs[0]
		f.putErrs = f.putErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	return &api.ActionResultDto{Success: true, Message: "ok"}, nil
}
func (f *fakeFileClient) ResetAppFile(_, path string) (*api.ActionResultDto, error) {
	f.resetPath = path
	return &api.ActionResultDto{Success: true, Message: "reset"}, nil
}
func (f *fakeFileClient) ReloadNamespace() (*api.ActionResultDto, error) {
	f.reloadHits++
	return &api.ActionResultDto{Success: true, Message: "reloaded"}, nil
}
func (f *fakeFileClient) ListAppFiles(string) ([]api.AppFileDto, error) {
	return f.listDTO, nil
}

func TestRunEditFile_Reset(t *testing.T) {
	f := &fakeFileClient{}
	if _, err := runEditFile(editFileOptions{app: "uiserv", path: "app/uiserv/props/application-launcher.yml", reset: true, cl: f}); err != nil {
		t.Fatal(err)
	}
	if f.resetPath != "app/uiserv/props/application-launcher.yml" {
		t.Errorf("reset path = %q", f.resetPath)
	}
	if f.reloadHits != 0 {
		t.Error("reset must not trigger a client-side reload (server reloads itself)")
	}
}

func TestRunEditFile_FromInputAppliesReload(t *testing.T) {
	f := &fakeFileClient{}
	o := editFileOptions{app: "uiserv", path: "p.yml", apply: true, file: strings.NewReader("debug: true\n"), cl: f}
	res, err := runEditFile(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.putCalls) != 1 || string(f.putCalls[0]) != "debug: true\n" {
		t.Errorf("putCalls = %q", f.putCalls)
	}
	if f.reloadHits != 1 {
		t.Errorf("expected one reload, got %d", f.reloadHits)
	}
	if !strings.Contains(res.Message, "applied") {
		t.Errorf("message = %q", res.Message)
	}
}

func TestRunEditFile_NoApplySkipsReload(t *testing.T) {
	f := &fakeFileClient{}
	o := editFileOptions{app: "uiserv", path: "p.yml", apply: false, file: strings.NewReader("x: 1\n"), cl: f}
	res, err := runEditFile(o)
	if err != nil {
		t.Fatal(err)
	}
	if f.reloadHits != 0 {
		t.Error("no reload expected with apply=false")
	}
	if !strings.Contains(res.Message, "citeck reload") {
		t.Errorf("message should hint at reload, got %q", res.Message)
	}
}

func TestRunEditFile_NonTTYNeedsFromOrReset(t *testing.T) {
	_, err := runEditFile(editFileOptions{app: "uiserv", path: "p.yml", isTTY: false, cl: &fakeFileClient{}})
	if err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("expected non-TTY error mentioning --from, got %v", err)
	}
}

func TestRunEditFile_NoChangeCancels(t *testing.T) {
	f := &fakeFileClient{getDTO: &api.AppFileContentDto{Content: "a: 1\n"}}
	o := editFileOptions{
		app: "uiserv", path: "p.yml", isTTY: true, cl: f,
		edit: func(b []byte) ([]byte, bool, error) { return b, false, nil },
	}
	if _, err := runEditFile(o); !errors.Is(err, errNoChanges) {
		t.Fatalf("expected errNoChanges, got %v", err)
	}
	if len(f.putCalls) != 0 {
		t.Error("no PUT expected when unchanged")
	}
}

func TestRunEditFile_ReeditOn400ThenSucceeds(t *testing.T) {
	f := &fakeFileClient{
		getDTO:  &api.AppFileContentDto{Content: "a: 1\n"},
		putErrs: []error{&client.APIError{Status: http.StatusBadRequest, Message: "invalid YAML"}, nil},
	}
	calls := 0
	o := editFileOptions{
		app: "uiserv", path: "p.yml", apply: true, isTTY: true, cl: f,
		edit: func(b []byte) ([]byte, bool, error) {
			calls++
			return append([]byte("edited"), b...), true, nil
		},
	}
	res, err := runEditFile(o)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(f.putCalls) != 2 {
		t.Errorf("calls=%d putCalls=%d", calls, len(f.putCalls))
	}
	// Reload happens only after the successful PUT, exactly once.
	if f.reloadHits != 1 {
		t.Errorf("expected one reload after success, got %d", f.reloadHits)
	}
	if !strings.Contains(res.Message, "applied") {
		t.Errorf("message = %q", res.Message)
	}
}

func TestIsEditableFile(t *testing.T) {
	editable := []string{
		"./app/uiserv/props/application-launcher.yml",
		"a.json", "b.conf", "c.sh", "Dockerfile", "d.txt", "e.lua",
	}
	for _, p := range editable {
		if !isEditableFile(p) {
			t.Errorf("%q should be editable", p)
		}
	}
	notEditable := []string{"cert.pem", "font.ttf", "lib.jar", "noext", "./x/Dockerfile.bak"}
	for _, p := range notEditable {
		if isEditableFile(p) {
			t.Errorf("%q should NOT be editable", p)
		}
	}
}
