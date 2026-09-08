package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newSlowDaemonClient is newTestClient with a plain HTTP client that times out
// almost immediately. Anything routed through it fails; anything routed through
// the timeout-free streaming client works. Measuring a data volume outlives any
// fixed client timeout — the daemon lifts its own write deadline for exactly
// that reason (routes_deps.go) — so the two long calls must not use httpClient.
func newSlowDaemonClient(srv *httptest.Server) *DaemonClient {
	return &DaemonClient{
		httpClient:   &http.Client{Timeout: time.Nanosecond, Transport: srv.Client().Transport},
		streamClient: srv.Client(),
		baseURL:      srv.URL,
	}
}

func TestGetDependencies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespace/dependencies" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"postgres","app":"postgres","currentImage":"postgres:17.5",` +
			`"targetImage":"postgres:18","status":"upgrade-available","migratable":true}],` +
			`"rollbackPending":"a rollback is pending"}`))
	}))
	defer srv.Close()

	dto, err := newTestClient(srv).GetDependencies()
	if err != nil {
		t.Fatal(err)
	}
	if len(dto.Items) != 1 || dto.Items[0].ID != "postgres" {
		t.Fatalf("items = %+v", dto.Items)
	}
	if dto.Items[0].TargetImage != "postgres:18" || !dto.Items[0].Migratable {
		t.Errorf("item = %+v", dto.Items[0])
	}
	if dto.RollbackPending != "a rollback is pending" {
		t.Errorf("rollbackPending = %q", dto.RollbackPending)
	}
}

func TestGetDependencies_ErrorMessageIsTheDaemons(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"NOT_CONFIGURED","message":"no namespace configured"}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).GetDependencies(); err == nil {
		t.Fatal("expected an error")
	} else if err.Error() != "no namespace configured" {
		t.Errorf("error = %q", err)
	}
}

func TestDependencyPreflight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespace/dependencies/postgres/preflight" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"from":"postgres:17.5","to":"postgres:18","dataSizeBytes":2147483648,` +
			`"existingTargetVolume":{"name":"citeck_pg18","sizeBytes":1024,"version":"18"},"wasRunning":true}`))
	}))
	defer srv.Close()

	// Through the SLOW client on purpose: the preflight measures a volume and
	// must not be cut off by the ordinary 120s request timeout.
	res, err := newSlowDaemonClient(srv).DependencyPreflight("postgres")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.From != "postgres:17.5" || res.To != "postgres:18" {
		t.Fatalf("result = %+v", res)
	}
	if res.DataSizeBytes != 2147483648 || !res.WasRunning {
		t.Errorf("result = %+v", res)
	}
	if res.ExistingTargetVolume == nil || res.ExistingTargetVolume.Name != "citeck_pg18" {
		t.Errorf("existingTargetVolume = %+v", res.ExistingTargetVolume)
	}
}

func TestMigrateDependency_PostsTheReplaceFlag(t *testing.T) {
	for _, replace := range []bool{true, false} {
		var gotBody string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/namespace/dependencies/postgres/migrate" {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("content-type = %q", ct)
			}
			body, _ := io.ReadAll(r.Body)
			gotBody = string(body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"success":true,"message":"Migration of postgres to postgres:18 started"}`))
		}))

		// Also through the slow client: the 202 is written only after the
		// daemon has built the plan, which runs the same measurement.
		res, err := newSlowDaemonClient(srv).MigrateDependency("postgres", replace)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		var req map[string]any
		if unmarshalErr := json.Unmarshal([]byte(gotBody), &req); unmarshalErr != nil {
			t.Fatalf("body %q: %v", gotBody, unmarshalErr)
		}
		if req["replaceExistingVolume"] != replace {
			t.Errorf("replaceExistingVolume = %v, want %v (body %s)", req["replaceExistingVolume"], replace, gotBody)
		}
		if !res.Success || res.Message == "" {
			t.Errorf("result = %+v", res)
		}
	}
}

func TestMigrateDependency_RefusalIsReturnedAsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"DEPENDENCY_NAMESPACE_BUSY","message":"the namespace is STARTING"}`))
	}))
	defer srv.Close()

	if _, err := newSlowDaemonClient(srv).MigrateDependency("postgres", false); err == nil {
		t.Fatal("expected an error")
	} else if err.Error() != "the namespace is STARTING" {
		t.Errorf("error = %q", err)
	}
}
