package h2migrate

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/citeck/citeck-launcher/internal/storage"
)

func TestDecryptSecretBlob_RealData(t *testing.T) {
	homeDir := os.ExpandEnv("$HOME/.citeck/launcher")
	store, err := storage.NewSQLiteStore(homeDir)
	if err != nil {
		t.Skip("no SQLite store:", err)
	}
	defer store.Close()

	blob, err := store.GetSecretBlob()
	if err != nil || blob == "" {
		t.Skip("no secret blob in store")
	}
	t.Logf("Blob length: %d", len(blob))

	result, err := DecryptSecretBlob(blob, "qwe")
	if err != nil {
		t.Fatalf("DecryptSecretBlob error: %v", err)
	}

	t.Logf("Decrypted %d keys", len(result))
	for k, v := range result {
		t.Logf("  %s: %s", k, string(v))
		// Try parse as different types
		var arr []json.RawMessage
		if json.Unmarshal(v, &arr) == nil {
			t.Logf("    → array of %d elements", len(arr))
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(v, &obj) == nil {
			t.Logf("    → object with %d keys", len(obj))
			for kk := range obj {
				t.Logf("      key: %s", kk)
			}
		}
	}
}
