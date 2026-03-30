package h2migrate

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/pbkdf2"

	"github.com/citeck/citeck-launcher/internal/storage"
)

// buildEncryptedBlob creates a real AES-256-GCM encrypted blob using the same
// PBKDF2+AES-GCM algorithm that the Kotlin launcher uses. This produces a valid
// EncryptedStorage JSON that DecryptSecretBlob can decrypt.
func buildEncryptedBlob(t *testing.T, plaintext []byte, password string) string {
	t.Helper()

	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	require.NoError(t, err)

	iterations := 1000
	keySize := 256
	keyBytes := keySize / 8
	key := pbkdf2.Key([]byte(password), salt, iterations, keyBytes, sha256.New)

	block, err := aes.NewCipher(key)
	require.NoError(t, err)

	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)

	iv := make([]byte, gcm.NonceSize())
	_, err = rand.Read(iv)
	require.NoError(t, err)

	ciphertext := gcm.Seal(nil, iv, plaintext, nil)

	es := EncryptedStorage{
		Key: KeyParams{
			Alg:        0,
			Salt:       base64.StdEncoding.EncodeToString(salt),
			KeySize:    keySize,
			Iterations: iterations,
		},
		Alg:    0,
		IV:     base64.StdEncoding.EncodeToString(iv),
		TagLen: 128,
		Data:   base64.StdEncoding.EncodeToString(ciphertext),
	}

	esJSON, err := json.Marshal(es)
	require.NoError(t, err)

	// The function expects the outer wrapper to be base64-encoded JSON
	return base64.StdEncoding.EncodeToString(esJSON)
}

func TestDecryptSecretBlob_KnownVector(t *testing.T) {
	password := "test-master-password"
	secretsPayload := map[string]any{
		"auth-secrets": map[string]AuthSecret{
			"repo1": {ID: "repo1", Type: "TOKEN", Token: "glpat-abc123"},
			"reg1":  {ID: "reg1", Type: "BASIC", Username: "admin", Password: "secret"},
		},
	}
	plaintext, err := json.Marshal(secretsPayload)
	require.NoError(t, err)

	blob := buildEncryptedBlob(t, plaintext, password)

	result, err := DecryptSecretBlob(blob, password)
	require.NoError(t, err)
	require.Contains(t, result, "auth-secrets")

	var secrets map[string]AuthSecret
	require.NoError(t, json.Unmarshal(result["auth-secrets"], &secrets))

	assert.Len(t, secrets, 2)
	assert.Equal(t, "TOKEN", secrets["repo1"].Type)
	assert.Equal(t, "glpat-abc123", secrets["repo1"].Token)
	assert.Equal(t, "BASIC", secrets["reg1"].Type)
	assert.Equal(t, "admin", secrets["reg1"].Username)
	assert.Equal(t, "secret", secrets["reg1"].Password)
}

func TestDecryptSecretBlob_WrongPassword(t *testing.T) {
	plaintext := []byte(`{"auth-secrets":{}}`)
	blob := buildEncryptedBlob(t, plaintext, "correct-password")

	_, err := DecryptSecretBlob(blob, "wrong-password")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt failed")
}

func TestDecryptSecretBlob_InvalidBase64(t *testing.T) {
	_, err := DecryptSecretBlob("not-valid-base64!!!", "password")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base64")
}

func TestDecryptSecretBlob_InvalidJSON(t *testing.T) {
	// Valid base64 but not valid JSON inside
	blob := base64.StdEncoding.EncodeToString([]byte("this is not json"))
	_, err := DecryptSecretBlob(blob, "password")
	require.Error(t, err)
}

func TestDecryptSecretBlob_UnsupportedAlgorithm(t *testing.T) {
	es := EncryptedStorage{
		Key: KeyParams{Alg: 99},
		Alg: 99,
	}
	esJSON, _ := json.Marshal(es)
	blob := base64.StdEncoding.EncodeToString(esJSON)

	_, err := DecryptSecretBlob(blob, "password")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported algorithm")
}

func TestDecryptSecretBlob_DoubleBase64(t *testing.T) {
	// Simulate the Kotlin double-base64 encoding: the outer base64 decodes
	// to a JSON string that is itself base64-encoded.
	password := "double-b64-test"
	plaintext := []byte(`{"auth-secrets":{}}`)
	blob := buildEncryptedBlob(t, plaintext, password)

	// blob is already base64(JSON). Now wrap it as base64(json.Marshal(base64(JSON)))
	// i.e., the outer decode yields a JSON string whose value is the inner base64.
	innerB64 := blob // this is base64(EncryptedStorage JSON)
	// Decode the outer blob to get the EncryptedStorage JSON
	esJSON, err := base64.StdEncoding.DecodeString(innerB64)
	require.NoError(t, err)

	// Now create the double-encoded form: base64(json.Marshal(base64(esJSON)))
	reEncoded := base64.StdEncoding.EncodeToString(esJSON)
	jsonStr, err := json.Marshal(reEncoded)
	require.NoError(t, err)
	doubleBlob := base64.StdEncoding.EncodeToString(jsonStr)

	result, err := DecryptSecretBlob(doubleBlob, password)
	require.NoError(t, err)
	require.Contains(t, result, "auth-secrets")
}

// --- ImportDecryptedSecrets tests ---

// mockStore implements storage.Store for testing ImportDecryptedSecrets.
type mockStore struct {
	secrets map[string]storage.Secret
}

func newMockStore() *mockStore {
	return &mockStore{secrets: make(map[string]storage.Secret)}
}

func (m *mockStore) SaveSecret(s storage.Secret) error      { m.secrets[s.ID] = s; return nil }
func (m *mockStore) ListSecrets() ([]storage.SecretMeta, error) { return nil, nil }
func (m *mockStore) GetSecret(id string) (*storage.Secret, error) {
	s, ok := m.secrets[id]
	if !ok {
		return nil, nil
	}
	return &s, nil
}
func (m *mockStore) DeleteSecret(string) error                   { return nil }
func (m *mockStore) ListWorkspaces() ([]storage.WorkspaceDto, error) { return nil, nil }
func (m *mockStore) GetWorkspace(string) (*storage.WorkspaceDto, error) { return nil, nil }
func (m *mockStore) SaveWorkspace(storage.WorkspaceDto) error    { return nil }
func (m *mockStore) DeleteWorkspace(string) error                { return nil }
func (m *mockStore) PutSecretBlob(string) error                  { return nil }
func (m *mockStore) GetSecretBlob() (string, error)              { return "", nil }
func (m *mockStore) GetState() (*storage.LauncherState, error)   { return &storage.LauncherState{}, nil }
func (m *mockStore) SetState(storage.LauncherState) error        { return nil }
func (m *mockStore) Close() error                                { return nil }

func TestImportDecryptedSecrets_TokenAndBasic(t *testing.T) {
	secretsMap := map[string]AuthSecret{
		"ws:test:repo": {ID: "ws:test:repo", Type: "TOKEN", Token: "glpat-xyz"},
		"images-repo:harbor.citeck.ru": {
			ID: "images-repo:harbor.citeck.ru", Type: "BASIC",
			Username: "admin", Password: "pass123",
		},
	}
	raw, err := json.Marshal(secretsMap)
	require.NoError(t, err)

	decrypted := map[string]json.RawMessage{
		"auth-secrets": raw,
	}

	store := newMockStore()
	count, err := ImportDecryptedSecrets(decrypted, store)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Verify TOKEN secret
	tokenSecret := store.secrets["ws:test:repo"]
	assert.Equal(t, storage.SecretGitToken, tokenSecret.Type)
	assert.Equal(t, "glpat-xyz", tokenSecret.Value)
	assert.Equal(t, "ws:test:repo", tokenSecret.Scope)

	// Verify BASIC secret
	basicSecret := store.secrets["images-repo:harbor.citeck.ru"]
	assert.Equal(t, storage.SecretRegistryAuth, basicSecret.Type)
	assert.Equal(t, "admin:pass123", basicSecret.Value)
}

func TestImportDecryptedSecrets_EmptyDecrypted(t *testing.T) {
	store := newMockStore()

	// No auth-secrets key at all
	count, err := ImportDecryptedSecrets(map[string]json.RawMessage{}, store)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestImportDecryptedSecrets_NilDecrypted(t *testing.T) {
	store := newMockStore()

	count, err := ImportDecryptedSecrets(nil, store)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestImportDecryptedSecrets_UnknownTypeSkipped(t *testing.T) {
	secretsMap := map[string]AuthSecret{
		"known":   {ID: "known", Type: "TOKEN", Token: "abc"},
		"unknown": {ID: "unknown", Type: "OAUTH2"},
	}
	raw, err := json.Marshal(secretsMap)
	require.NoError(t, err)

	decrypted := map[string]json.RawMessage{
		"auth-secrets": raw,
	}

	store := newMockStore()
	count, err := ImportDecryptedSecrets(decrypted, store)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Contains(t, store.secrets, "known")
	assert.NotContains(t, store.secrets, "unknown")
}

func TestImportDecryptedSecrets_EmptySecretsMap(t *testing.T) {
	raw, _ := json.Marshal(map[string]AuthSecret{})
	decrypted := map[string]json.RawMessage{
		"auth-secrets": raw,
	}

	store := newMockStore()
	count, err := ImportDecryptedSecrets(decrypted, store)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
