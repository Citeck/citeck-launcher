package namespace

import (
	"crypto/rand"
	"fmt"
)

const (
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
	return generatePassword(adminPasswordLength, adminPasswordAlphabet)
}

// citeckSAPasswordLength is 32 chars from the full alphanumeric alphabet
// (~190 bits of entropy). The SA password is never shown to users — only
// stored encrypted in the secret store.
const citeckSAPasswordLength = 32

// GenerateCiteckSAPassword generates a strong random password for the
// "citeck" service account. The same password is used in the Keycloak
// master realm (admin role) and in RabbitMQ (monitoring tag, vhost "/"
// full permissions), so admin-password changes and snapshot imports
// don't churn the webapp container spec.
func GenerateCiteckSAPassword() (string, error) {
	return generatePassword(citeckSAPasswordLength, adminPasswordAlphabet)
}

func generatePassword(length int, alphabet string) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}
