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

// ErrAlreadyEncrypted is returned when SetMasterPassword is called but encryption
// is already configured.
var ErrAlreadyEncrypted = errors.New("encryption already configured")

// ErrCorruptedKeystore is returned when encryption metadata is missing or unreadable.
var ErrCorruptedKeystore = errors.New("keystore is corrupted or missing key params")

// DefaultMasterPassword is the well-known default password used for secret
// encryption when no custom password has been set. Exported so that both the
// daemon (server.go) and the CLI (start.go) reference the same value.
const DefaultMasterPassword = "citeck" //nolint:gosec // G101: well-known default, not a secret

const (
	verifyPlaintext      = "citeck-secrets-v1"
	defaultIterations    = 1_000_000
	stateEncrypted       = "secrets_encrypted"
	stateKeyParams       = "secrets_key_params"
	stateVerify          = "secrets_verify"
	stateDefaultPassword = "secrets_default_password"
)

// CryptoKeyParams holds the PBKDF2 parameters used to derive the encryption key.
type CryptoKeyParams struct {
	Salt       string `json:"salt"`       // base64-encoded 16-byte random salt
	Iterations int    `json:"iterations"` // PBKDF2 iteration count
	KeySize    int    `json:"keySize"`    // key size in bits (256)
}

// SecretService wraps a Store and adds transparent AES-256-GCM
// encryption/decryption for secret values.
type SecretService struct {
	store      Store
	mu         sync.RWMutex
	derivedKey []byte // 32-byte AES key; nil when locked
	encrypted  bool   // true when secrets_encrypted == "true" in state
}

// NewSecretService creates a SecretService wrapping the given Store.
// It reads encryption state from key-value state on creation.
func NewSecretService(store Store) (*SecretService, error) {
	ss := &SecretService{store: store}
	enc, err := store.GetStateValue(stateEncrypted)
	if err != nil {
		return nil, fmt.Errorf("read encryption state: %w", err)
	}
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
// and encrypts all existing plaintext secrets.
// If isDefault is true, the default password flag is stored so CLI can auto-unlock.
func (ss *SecretService) SetMasterPassword(password string, isDefault bool) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	if ss.encrypted {
		return ErrAlreadyEncrypted
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

	// Encrypt all existing secrets via Store interface
	metas, err := ss.store.ListSecrets()
	if err != nil {
		return fmt.Errorf("list secrets: %w", err)
	}
	for _, meta := range metas {
		sec, getErr := ss.store.GetSecret(meta.ID)
		if getErr != nil {
			return fmt.Errorf("read secret %s: %w", meta.ID, getErr)
		}
		encrypted, encErr := encryptValue(key, []byte(sec.Value))
		if encErr != nil {
			return fmt.Errorf("encrypt secret %s: %w", meta.ID, encErr)
		}
		sec.Value = encrypted
		if saveErr := ss.store.SaveSecret(*sec); saveErr != nil {
			return fmt.Errorf("save secret %s: %w", meta.ID, saveErr)
		}
	}

	// Store metadata via key-value state.
	// Set secrets_encrypted LAST — if we crash before this, retry will re-encrypt.
	keyParams := CryptoKeyParams{
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Iterations: defaultIterations,
		KeySize:    256,
	}
	paramsJSON, err := json.Marshal(keyParams)
	if err != nil {
		return fmt.Errorf("marshal key params: %w", err)
	}
	if err := ss.store.SetStateValue(stateKeyParams, string(paramsJSON)); err != nil {
		return fmt.Errorf("set %s: %w", stateKeyParams, err)
	}
	if err := ss.store.SetStateValue(stateVerify, verifyEncrypted); err != nil {
		return fmt.Errorf("set %s: %w", stateVerify, err)
	}
	defaultPwd := "false"
	if isDefault {
		defaultPwd = "true"
	}
	if err := ss.store.SetStateValue(stateDefaultPassword, defaultPwd); err != nil {
		return fmt.Errorf("set %s: %w", stateDefaultPassword, err)
	}
	if err := ss.store.SetStateValue(stateEncrypted, "true"); err != nil {
		return fmt.Errorf("set %s: %w", stateEncrypted, err)
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
		return fmt.Errorf("%w: missing key params", ErrCorruptedKeystore)
	}
	var params CryptoKeyParams
	if unmarshalErr := json.Unmarshal([]byte(paramsStr), &params); unmarshalErr != nil {
		return fmt.Errorf("%w: parse key params", ErrCorruptedKeystore)
	}

	salt, err := base64.StdEncoding.DecodeString(params.Salt)
	if err != nil {
		return fmt.Errorf("%w: decode salt", ErrCorruptedKeystore)
	}

	key := deriveKey(password, salt, params.Iterations)

	verifyEnc, err := ss.store.GetStateValue(stateVerify)
	if err != nil || verifyEnc == "" {
		return fmt.Errorf("%w: missing verify token", ErrCorruptedKeystore)
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
		return nil, fmt.Errorf("get secret %s: %w", id, err)
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
// Uses RLock because it only reads derivedKey/encrypted — the underlying SQLite
// DB serializes concurrent writes via MaxOpenConns(1).
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
	if err := ss.store.SaveSecret(secret); err != nil {
		return fmt.Errorf("save secret: %w", err)
	}
	return nil
}

// ListSecrets passes through to the underlying store (metadata only, no decryption).
func (ss *SecretService) ListSecrets() ([]SecretMeta, error) {
	metas, err := ss.store.ListSecrets()
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	return metas, nil
}

// DeleteSecret passes through to the underlying store.
func (ss *SecretService) DeleteSecret(id string) error {
	if err := ss.store.DeleteSecret(id); err != nil {
		return fmt.Errorf("delete secret %s: %w", id, err)
	}
	return nil
}

// IsDefaultPassword returns true if the default password flag is set.
func (ss *SecretService) IsDefaultPassword() bool {
	val, _ := ss.store.GetStateValue(stateDefaultPassword)
	return val == "true"
}

// ResetSecrets deletes all secrets and clears encryption metadata.
// Used when the user forgets the master password and wants to start over.
func (ss *SecretService) ResetSecrets() error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	metas, err := ss.store.ListSecrets()
	if err != nil {
		return fmt.Errorf("list secrets: %w", err)
	}
	for _, meta := range metas {
		if delErr := ss.store.DeleteSecret(meta.ID); delErr != nil {
			return fmt.Errorf("delete secret %s: %w", meta.ID, delErr)
		}
	}

	for _, key := range []string{stateEncrypted, stateKeyParams, stateVerify, stateDefaultPassword} {
		if setErr := ss.store.SetStateValue(key, ""); setErr != nil {
			return fmt.Errorf("clear %s: %w", key, setErr)
		}
	}

	ss.encrypted = false
	ss.derivedKey = nil
	return nil
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
