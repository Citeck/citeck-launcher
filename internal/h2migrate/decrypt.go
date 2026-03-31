package h2migrate

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/pbkdf2"

	"github.com/citeck/citeck-launcher/internal/storage"
)

// EncryptedStorage matches the Kotlin EncryptedStorage class.
type EncryptedStorage struct {
	Key    KeyParams `json:"key"`
	Alg    int       `json:"alg"`
	IV     string    `json:"iv"`     // base64
	TagLen int       `json:"tagLen"` // bits
	Data   string    `json:"data"`   // base64
}

// KeyParams matches the Kotlin KeyParams class.
type KeyParams struct {
	Alg        int    `json:"alg"`
	Salt       string `json:"salt"` // base64
	KeySize    int    `json:"keySize"`
	Iterations int    `json:"iterations"`
}

// DecryptedSecrets is the result of decrypting the Kotlin secrets blob.
type DecryptedSecrets struct {
	AuthSecrets []AuthSecret `json:"auth-secrets"`
}

// AuthSecret represents a Kotlin auth secret (token or basic).
type AuthSecret struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "token" or "basic"
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Version  int64  `json:"version,omitempty"`
}

// DecryptSecretBlob decrypts the Kotlin EncryptedStorage blob using the master password.
// Returns the decrypted JSON as a map (keys like "auth-secrets" → JSON array).
func DecryptSecretBlob(blobBase64, masterPassword string) (map[string]json.RawMessage, error) {
	// Decode the outer base64 wrapping (the blob is double-base64 encoded in migration)
	outerJSON, err := base64.StdEncoding.DecodeString(blobBase64)
	if err != nil {
		return nil, fmt.Errorf("decode outer base64: %w", err)
	}

	// The outer JSON might itself be a base64-encoded string (from Kotlin's JSON serialization)
	var innerB64 string
	if json.Unmarshal(outerJSON, &innerB64) == nil && innerB64 != "" {
		outerJSON, err = base64.StdEncoding.DecodeString(innerB64)
		if err != nil {
			return nil, fmt.Errorf("decode inner base64: %w", err)
		}
	}

	var es EncryptedStorage
	if unmarshalErr := json.Unmarshal(outerJSON, &es); unmarshalErr != nil {
		return nil, fmt.Errorf("parse EncryptedStorage: %w", unmarshalErr)
	}

	if es.Alg != 0 || es.Key.Alg != 0 {
		return nil, fmt.Errorf("unsupported algorithm: storage=%d, key=%d", es.Alg, es.Key.Alg)
	}

	// Derive AES key from master password via PBKDF2
	salt, err := base64.StdEncoding.DecodeString(es.Key.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	keyBytes := es.Key.KeySize / 8 // 256 bits → 32 bytes
	key := pbkdf2.Key([]byte(masterPassword), salt, es.Key.Iterations, keyBytes, sha256.New)

	// Decrypt with AES-GCM
	iv, err := base64.StdEncoding.DecodeString(es.IV)
	if err != nil {
		return nil, fmt.Errorf("decode IV: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(es.Data)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed (wrong password?): %w", err)
	}

	// Parse decrypted JSON
	var result map[string]json.RawMessage
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return nil, fmt.Errorf("parse decrypted data: %w", err)
	}
	return result, nil
}

// ImportDecryptedSecrets parses auth secrets from decrypted data and saves them to the store.
// Kotlin stores auth-secrets as a map: { "secretId": { type, id, token/username/password } }
func ImportDecryptedSecrets(decrypted map[string]json.RawMessage, store storage.Store) (int, error) {
	raw, ok := decrypted["auth-secrets"]
	if !ok {
		return 0, nil
	}

	var secretsMap map[string]AuthSecret
	if err := json.Unmarshal(raw, &secretsMap); err != nil {
		return 0, fmt.Errorf("parse auth-secrets: %w", err)
	}

	count := 0
	for id, s := range secretsMap {
		secret := storage.Secret{
			SecretMeta: storage.SecretMeta{
				ID:   id,
				Name: id,
			},
		}
		switch s.Type {
		case "TOKEN":
			secret.Type = storage.SecretGitToken
			secret.Value = s.Token
			secret.Scope = id // e.g. "ws:py2iwgi:repo"
		case "BASIC":
			secret.Type = storage.SecretRegistryAuth
			secret.Value = s.Username + ":" + s.Password
			secret.Scope = id // e.g. "images-repo:harbor.citeck.ru"
		default:
			continue
		}
		if err := store.SaveSecret(secret); err != nil {
			continue
		}
		count++
	}
	return count, nil
}
