package cli

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/client"
)

type fakeConfigClient struct {
	getDTO   *api.AppConfigDto
	putCalls [][]byte
	putErrs  []error // consumed per PutAppConfig call
	resetHit bool
}

func (f *fakeConfigClient) GetAppConfig(string) (*api.AppConfigDto, error) {
	return f.getDTO, nil
}
func (f *fakeConfigClient) PutAppConfig(_ string, body []byte) (*api.ActionResultDto, error) {
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
func (f *fakeConfigClient) ResetAppConfig(string) (*api.ActionResultDto, error) {
	f.resetHit = true
	return &api.ActionResultDto{Success: true, Message: "reset"}, nil
}

func TestRunEdit_Reset(t *testing.T) {
	f := &fakeConfigClient{}
	if _, err := runEdit(editOptions{app: "rabbitmq", reset: true, cl: f}); err != nil {
		t.Fatal(err)
	}
	if !f.resetHit {
		t.Error("reset endpoint not called")
	}
}

func TestRunEdit_FileInput(t *testing.T) {
	f := &fakeConfigClient{}
	o := editOptions{app: "rabbitmq", file: strings.NewReader("name: rabbitmq\n"), cl: f}
	if _, err := runEdit(o); err != nil {
		t.Fatal(err)
	}
	if len(f.putCalls) != 1 || string(f.putCalls[0]) != "name: rabbitmq\n" {
		t.Errorf("putCalls = %q", f.putCalls)
	}
}

func TestRunEdit_NonTTYInteractive(t *testing.T) {
	_, err := runEdit(editOptions{app: "rabbitmq", isTTY: false, cl: &fakeConfigClient{}})
	if err == nil || !strings.Contains(err.Error(), "--file") {
		t.Fatalf("expected non-TTY error mentioning --file, got %v", err)
	}
}

func TestRunEdit_NoChangeCancels(t *testing.T) {
	f := &fakeConfigClient{getDTO: &api.AppConfigDto{Content: "name: rabbitmq\n"}}
	o := editOptions{
		app: "rabbitmq", isTTY: true, cl: f,
		edit: func(b []byte) ([]byte, bool, error) { return b, false, nil },
	}
	if _, err := runEdit(o); !errors.Is(err, errNoChanges) {
		t.Fatalf("expected errNoChanges, got %v", err)
	}
	if len(f.putCalls) != 0 {
		t.Error("no PUT expected when unchanged")
	}
}

func TestRunEdit_ReeditOn400ThenSucceeds(t *testing.T) {
	f := &fakeConfigClient{
		getDTO:  &api.AppConfigDto{Content: "name: rabbitmq\n"},
		putErrs: []error{&client.APIError{Status: http.StatusBadRequest, Message: "invalid YAML"}, nil},
	}
	calls := 0
	o := editOptions{
		app: "rabbitmq", isTTY: true, cl: f,
		edit: func(b []byte) ([]byte, bool, error) {
			calls++
			// Always "change" the buffer so both rounds attempt a PUT.
			return append([]byte("edited"), b...), true, nil
		},
	}
	res, err := runEdit(o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message != "ok" || calls != 2 || len(f.putCalls) != 2 {
		t.Errorf("calls=%d putCalls=%d res=%+v", calls, len(f.putCalls), res)
	}
}
