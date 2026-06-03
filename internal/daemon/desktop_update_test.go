package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/citeck/citeck-launcher/internal/update"
)

func TestHandleUpdateStatusNotAvailableByDefault(t *testing.T) {
	d := &Daemon{updateSvc: update.NewService("2.4.0", t.TempDir())}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/desktop/update/status", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleUpdateStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d", rec.Code)
	}
	var st update.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.CurrentVersion != "2.4.0" || st.Available {
		t.Fatalf("status = %+v", st)
	}
}
