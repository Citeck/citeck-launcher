package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/citeck/citeck-launcher/internal/update/updatetest"
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

// TestHandleUpdateStatusManualUpdateFlag verifies the manual-update
// classification reaches the UI through the status endpoint after a staging
// attempt fails signature verification (e.g. signing-key rotation: the release
// ships no .sig / a .sig the embedded key cannot verify).
func TestHandleUpdateStatusManualUpdateFlag(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fake := updatetest.Start(t, "Citeck/citeck-launcher",
		updatetest.Release{Version: "2.6.0", Date: "2026-06-01", BinaryContent: "unsigned-daemon"},
	)
	svc := update.NewService("2.4.0", t.TempDir(),
		append(fake.Options(), update.WithSigningPublicKeyHex(hex.EncodeToString(pub)))...)
	if _, stageErr := svc.Stage(t.Context()); stageErr == nil {
		t.Fatal("Stage must reject the unsigned release")
	}

	d := &Daemon{updateSvc: svc}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/desktop/update/status", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleUpdateStatus(rec, req)

	var st update.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.ManualUpdateRequired || st.ManualUpdateReason != update.ReasonSignatureMissing {
		t.Fatalf("status = %+v; want manualUpdateRequired with reason %q", st, update.ReasonSignatureMissing)
	}
	if st.ReleasesURL == "" {
		t.Fatal("status must carry a releases URL for the manual download button")
	}
}
