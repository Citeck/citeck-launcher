package namespace

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// Keycloak imports a realm from JSON on first startup. The admin user's
// credential is a PBKDF2-SHA256 hash stored in the `secretData` field of
// that realm file. This module generates simple readable passwords for the
// ecos-app realm admin and produces the credential JSON that Keycloak
// expects.

const (
	// kcHashIterations matches the default on the Keycloak version bundled
	// with the platform (26.x): 27500 rounds of PBKDF2-SHA256. Kept in sync
	// with the pre-baked hash in internal/appfiles/keycloak/ecos-app-realm.json.
	kcHashIterations = 27500
	kcHashAlgorithm  = "pbkdf2-sha256"
	kcSaltBytes      = 16
	kcHashBytes      = 64 // pbkdf2 key length; matches what Keycloak expects for SHA-256

	// adminPasswordLength is the length of generated passwords. 10 chars
	// from a 53-symbol alphabet gives ~57 bits of entropy — plenty to
	// resist brute force on a platform that isn't exposed to the internet
	// by default, without being too awkward to read off a terminal.
	adminPasswordLength = 10
)

// adminPasswordAlphabet excludes visually ambiguous characters (0/O, 1/l/I)
// so users can read the generated password from a terminal and retype it
// without transcription errors.
const adminPasswordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"

// GenerateSimpleAdminPassword returns a fresh random password suitable for
// the ecos-app realm admin user. The alphabet avoids ambiguous characters.
func GenerateSimpleAdminPassword() (string, error) {
	buf := make([]byte, adminPasswordLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	out := make([]byte, adminPasswordLength)
	for i, b := range buf {
		out[i] = adminPasswordAlphabet[int(b)%len(adminPasswordAlphabet)]
	}
	return string(out), nil
}

// keycloakSecretData is the JSON shape Keycloak expects inside the
// `secretData` string of a credential entry.
type keycloakSecretData struct {
	Value string `json:"value"`
	Salt  string `json:"salt"`
}

// keycloakCredentialData is the JSON shape Keycloak expects inside the
// `credentialData` string of a credential entry.
type keycloakCredentialData struct {
	HashIterations int    `json:"hashIterations"`
	Algorithm      string `json:"algorithm"`
}

// Template placeholders present in internal/appfiles/keycloak/ecos-app-realm.json.
// substituteAdminPasswordHash replaces these with a freshly hashed password
// so the generated realm.json bootstraps keycloak with our chosen credential
// on the first container start. The template `requiredActions` entry is also
// cleared so the user can log in directly with the generated password instead
// of being forced to change it on first login (the generated password is
// already strong enough; `citeck setup admin-password` is the change path).
//
//nolint:gosec // G101: template placeholders from the bundled realm.json, not real credentials
const (
	kcTemplateSecretData     = `"secretData" : "{\"value\":\"31MudlHfx763mvpL+ZJU/etLgu2qFt0NpX+xyK0f8MCxOagc5N5+LCLQ/lUx8PJA6+eflW/eRQcnqGR7L8qb7w==\",\"salt\":\"SWU35ecF1zm/VYEcvzdNJg==\"}"`
	kcTemplateCredentialData = `"credentialData" : "{\"hashIterations\":27500,\"algorithm\":\"pbkdf2-sha256\"}"`
	kcTemplateRequiredAction = `"requiredActions" : [ "UPDATE_PASSWORD" ]`
)

// substituteAdminPasswordHash replaces the placeholder hash in the bundled
// realm.json template with a fresh PBKDF2 hash of the given password. The
// UPDATE_PASSWORD required action is cleared so the user can log in directly
// with the generated password. Returns the original realm unchanged if the
// hash operation fails (defensive — daemon startup should never abort on a
// realm substitution error; realm.json will just have its template default).
func substituteAdminPasswordHash(realm, password string) string {
	secretData, credentialData, err := HashKeycloakPBKDF2(password)
	if err != nil {
		return realm
	}
	// JSON-encode the inner blob values so they embed safely as string
	// values (backslash-escape quotes) inside the outer realm JSON.
	sdEscaped := jsonEscapeInner(secretData)
	cdEscaped := jsonEscapeInner(credentialData)

	realm = strings.Replace(realm,
		kcTemplateSecretData,
		`"secretData" : "`+sdEscaped+`"`, 1)
	realm = strings.Replace(realm,
		kcTemplateCredentialData,
		`"credentialData" : "`+cdEscaped+`"`, 1)
	realm = strings.Replace(realm,
		kcTemplateRequiredAction,
		`"requiredActions" : [ ]`, 1)
	return realm
}

// jsonEscapeInner escapes a JSON document so it can be embedded as a string
// value inside another JSON document. Equivalent to json.Marshal's string
// encoding but without the surrounding double quotes.
func jsonEscapeInner(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	// b is `"...escaped..."` — strip the surrounding quotes.
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		return string(b[1 : len(b)-1])
	}
	return string(b)
}

// HashKeycloakPBKDF2 produces the (secretData, credentialData) JSON strings
// that go into a Keycloak realm export credential entry. The caller embeds
// them as string values into the existing users[].credentials[] array.
//
// Format (Keycloak 26.x default PBKDF2-SHA256 provider):
//
//	secretData:     {"value":"<base64(hash)>","salt":"<base64(salt)>"}
//	credentialData: {"hashIterations":27500,"algorithm":"pbkdf2-sha256"}
//
// The random 16-byte salt is generated fresh per call.
func HashKeycloakPBKDF2(password string) (secretData, credentialData string, err error) {
	salt := make([]byte, kcSaltBytes)
	if _, saltErr := rand.Read(salt); saltErr != nil {
		return "", "", fmt.Errorf("read random salt: %w", saltErr)
	}
	hash := pbkdf2.Key([]byte(password), salt, kcHashIterations, kcHashBytes, sha256.New)

	sd, err := json.Marshal(keycloakSecretData{
		Value: base64.StdEncoding.EncodeToString(hash),
		Salt:  base64.StdEncoding.EncodeToString(salt),
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal secretData: %w", err)
	}
	cd, err := json.Marshal(keycloakCredentialData{
		HashIterations: kcHashIterations,
		Algorithm:      kcHashAlgorithm,
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal credentialData: %w", err)
	}
	return string(sd), string(cd), nil
}
