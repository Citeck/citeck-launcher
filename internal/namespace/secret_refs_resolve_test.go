package namespace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ResolveSecretRefs is the generator's substitution, plus the ids that had no
// value — the daemon refuses to hand ECOS a credential that still carries one.
func TestResolveSecretRefs(t *testing.T) {
	values := map[string]string{"seaweedfs-s3": "s3cret", "empty": ""}

	got, missing := ResolveSecretRefs("${secret:seaweedfs-s3}", values)
	assert.Equal(t, "s3cret", got)
	assert.Empty(t, missing)

	got, missing = ResolveSecretRefs("citeck", values)
	assert.Equal(t, "citeck", got, "a literal is a literal")
	assert.Empty(t, missing)

	got, missing = ResolveSecretRefs("${secret:empty}", values)
	assert.Empty(t, got, "an empty value is a value")
	assert.Empty(t, missing)

	got, missing = ResolveSecretRefs("${secret:b}-${secret:a}-${secret:b}-${secret:seaweedfs-s3}", values)
	assert.Equal(t, "${secret:b}-${secret:a}-${secret:b}-s3cret", got, "an unresolved reference stays in place")
	assert.Equal(t, []string{"a", "b"}, missing, "sorted, without duplicates")
}
