package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/pbkdf2"
)

// ErrSecretsLocked is returned when secrets are encrypted but the master key
// has not been provided yet (via Unlock or SetMasterPassword).
var ErrSecretsLocked = errors.New("secrets are locked: master password required")

const (
	verifyPlaintext    = "citeck-secrets-v1"
	defaultIterations  = 1_000_000
	stateEncrypted     = "secrets_encrypted"
	stateKeyParams     = "secrets_key_params"
	stateVerify        = "secrets_verify"
)

// CryptoKeyParams holds the PBKDF2 parameters used to derive the encryption key.
type CryptoKeyParams struct {
	Salt       string `json:"salt"`       // base64-encoded 16-byte random salt
	Iterations int    `json:"iterations"` // PBKDF2 iteration count
	KeySize    int    `json:"keySize"`    // key size in bits (256)
}

// SecretService wraps a SQLiteStore and adds transparent AES-256-GCM
// encryption/decryption for secret values. Used in desktop mode only.
type SecretService struct {
	store      *SQLiteStore
	mu         sync.RWMutex
	derivedKey []byte // 32-byte AES key; nil when locked
	encrypted  bool   // true when secrets_encrypted == "true" in launcher_state
}

// NewSecretService creates a SecretService wrapping the given SQLiteStore.
// It reads encryption state from launcher_state on creation.
func NewSecretService(store *SQLiteStore) (*SecretService, error) {
	ss := &SecretService{store: store}
	enc, _ := store.GetStateValue(stateEncrypted)
	ss.encrypted = (enc == "true")
	return ss, nil
}

// IsEncrypted returns true if encryption is configured.
func (ss *SecretService) IsEncrypted() bool {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.encrypted
}

// IsLocked returns true if encrypted but no key has been provided.
func (ss *SecretService) IsLocked() bool {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.encrypted && ss.derivedKey == nil
}

// SetMasterPassword sets up encryption for the first time.
// Generates a random salt, derives a key, creates a verify token,
// and encrypts all existing plaintext secrets in a single transaction.
func (ss *SecretService) SetMasterPassword(password string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	if ss.encrypted {
		return fmt.Errorf("encryption already configured")
	}

	// Generate 16-byte random salt
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}

	key := deriveKey(password, salt, defaultIterations)

	// Create verify token
	verifyEncrypted, err := encryptValue(key, []byte(verifyPlaintext))
	if err != nil {
		return fmt.Errorf("create verify token: %w", err)
	}

	// Encrypt all existing secrets in a transaction
	db := ss.store.DB()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query("SELECT id, value FROM secrets")
	if err != nil {
		return fmt.Errorf("read secrets: %w", err)
	}
	type idValue struct{ id, value string }
	var secrets []idValue
	for rows.Next() {
		var iv idValue
		if err := rows.Scan(&iv.id, &iv.value); err != nil {
			rows.Close()
			return fmt.Errorf("scan secret: %w", err)
		}
		secrets = append(secrets, iv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate secrets: %w", err)
	}

	for _, s := range secrets {
		enc, err := encryptValue(key, []byte(s.value))
		if err != nil {
			return fmt.Errorf("encrypt secret %s: %w", s.id, err)
		}
		if _, err := tx.Exec("UPDATE secrets SET value = ? WHERE id = ?", enc, s.id); err != nil {
			return fmt.Errorf("update secret %s: %w", s.id, err)
		}
	}

	// Store metadata in launcher_state
	upsert := `INSERT INTO launcher_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`
	keyParams := CryptoKeyParams{
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Iterations: defaultIterations,
		KeySize:    256,
	}
	paramsJSON, _ := json.Marshal(keyParams)

	if _, err := tx.Exec(upsert, stateEncrypted, "true"); err != nil {
		return fmt.Errorf("set %s: %w", stateEncrypted, err)
	}
	if _, err := tx.Exec(upsert, stateKeyParams, string(paramsJSON)); err != nil {
		return fmt.Errorf("set %s: %w", stateKeyParams, err)
	}
	if _, err := tx.Exec(upsert, stateVerify, verifyEncrypted); err != nil {
		return fmt.Errorf("set %s: %w", stateVerify, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	ss.derivedKey = key
	ss.encrypted = true
	return nil
}

// Unlock derives the key from the stored salt and validates it against the verify token.
func (ss *SecretService) Unlock(password string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	if !ss.encrypted {
		return fmt.Errorf("secrets are not encrypted")
	}
	if ss.derivedKey != nil {
		return nil // already unlocked
	}

	paramsStr, err := ss.store.GetStateValue(stateKeyParams)
	if err != nil || paramsStr == "" {
		return fmt.Errorf("missing key params")
	}
	var params CryptoKeyParams
	if err := json.Unmarshal([]byte(paramsStr), &params); err != nil {
		return fmt.Errorf("parse key params: %w", err)
	}

	salt, err := base64.StdEncoding.DecodeString(params.Salt)
	if err != nil {
		return fmt.Errorf("decode salt: %w", err)
	}

	key := deriveKey(password, salt, params.Iterations)

	verifyEnc, err := ss.store.GetStateValue(stateVerify)
	if err != nil || verifyEnc == "" {
		return fmt.Errorf("missing verify token")
	}

	plaintext, err := decryptValue(key, verifyEnc)
	if err != nil {
		return fmt.Errorf("invalid password")
	}
	if plaintext != verifyPlaintext {
		return fmt.Errorf("invalid password")
	}

	ss.derivedKey = key
	return nil
}

// GetSecret reads a secret from the store and decrypts the value if encryption is active.
func (ss *SecretService) GetSecret(id string) (*Secret, error) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	sec, err := ss.store.GetSecret(id)
	if err != nil {
		return nil, err
	}

	if ss.encrypted {
		if ss.derivedKey == nil {
			return nil, ErrSecretsLocked
		}
		plaintext, err := decryptValue(ss.derivedKey, sec.Value)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret %s: %w", id, err)
		}
		sec.Value = plaintext
	}
	return sec, nil
}

// SaveSecret encrypts the value (if encryption is active) and saves to the store.
func (ss *SecretService) SaveSecret(secret Secret) error {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	if ss.encrypted {
		if ss.derivedKey == nil {
			return ErrSecretsLocked
		}
		enc, err := encryptValue(ss.derivedKey, []byte(secret.Value))
		if err != nil {
			return fmt.Errorf("encrypt secret: %w", err)
		}
		secret.Value = enc
	}
	return ss.store.SaveSecret(secret)
}

// ListSecrets passes through to the underlying store (metadata only, no decryption).
func (ss *SecretService) ListSecrets() ([]SecretMeta, error) {
	return ss.store.ListSecrets()
}

// DeleteSecret passes through to the underlying store.
func (ss *SecretService) DeleteSecret(id string) error {
	return ss.store.DeleteSecret(id)
}

// Store returns the underlying Store for operations that don't involve secret values.
func (ss *SecretService) Store() Store {
	return ss.store
}

// --- Crypto primitives ---

// deriveKey derives a 32-byte AES-256 key from a password using PBKDF2-HMAC-SHA256.
func deriveKey(password string, salt []byte, iterations int) []byte {
	return pbkdf2.Key([]byte(password), salt, iterations, 32, sha256.New)
}

// encryptValue encrypts plaintext with AES-256-GCM.
// Returns base64(12-byte-IV || ciphertext || 16-byte-tag).
func encryptValue(key, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	iv := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("generate IV: %w", err)
	}
	ciphertext := gcm.Seal(iv, iv, plaintext, nil) // prepends iv to output
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptValue reverses encryptValue.
// Input is base64(12-byte-IV || ciphertext || 16-byte-tag).
func decryptValue(key []byte, encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	plaintext, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}
