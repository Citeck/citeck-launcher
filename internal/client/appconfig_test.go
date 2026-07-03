package client

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(srv *httptest.Server) *DaemonClient {
	return &DaemonClient{httpClient: srv.Client(), streamClient: srv.Client(), baseURL: srv.URL}
}

func TestGetAppConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/apps/rabbitmq/config" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"content":"name: rabbitmq\n","baseline":"name: rabbitmq\n"}`))
	}))
	defer srv.Close()

	dto, err := newTestClient(srv).GetAppConfig("rabbitmq")
	if err != nil {
		t.Fatal(err)
	}
	if dto.Content != "name: rabbitmq\n" {
		t.Errorf("content = %q", dto.Content)
	}
}

func TestPutAppConfig_SendsRawYAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/apps/rabbitmq/config" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "text/yaml" {
			t.Errorf("content-type = %q, want text/yaml", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "name: rabbitmq\nresources:\n  limits:\n    memory: 2g\n" {
			t.Errorf("body = %q (must be raw YAML, not JSON)", string(body))
		}
		_, _ = w.Write([]byte(`{"success":true,"message":"restart requested"}`))
	}))
	defer srv.Close()

	res, err := newTestClient(srv).PutAppConfig("rabbitmq",
		[]byte("name: rabbitmq\nresources:\n  limits:\n    memory: 2g\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Error("expected success")
	}
}

func TestPutAppConfig_400ReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid YAML: bad indent"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).PutAppConfig("rabbitmq", []byte("nope"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusBadRequest || apiErr.Message != "invalid YAML: bad indent" {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestResetAppConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/apps/rabbitmq/config/reset" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"success":true,"message":"reset"}`))
	}))
	defer srv.Close()

	res, err := newTestClient(srv).ResetAppConfig("rabbitmq")
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
