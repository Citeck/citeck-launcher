package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSummarizeHashDiff_ChangedAddedRemoved(t *testing.T) {
	ensureI18n()
	added := []string{"imageDigest=sha256:new", "env:ECOS_NEW=1"}
	removed := []string{"imageDigest=sha256:old", "env:ECOS_GONE=0"}

	got := summarizeHashDiff(added, removed, 10)
	assert.Equal(t, "~ imageDigest, + env:ECOS_NEW, - env:ECOS_GONE", got)
}

func TestSummarizeHashDiff_TruncatesWithMore(t *testing.T) {
	ensureI18n()
	added := []string{"env:A=1", "env:B=2", "env:C=3", "env:D=4", "env:E=5"}

	got := summarizeHashDiff(added, nil, 3)
	assert.Equal(t, "+ env:A, + env:B, + env:C, +2 more", got)
}

func TestSummarizeHashDiff_DuplicateKeysCollapse(t *testing.T) {
	ensureI18n()
	// Two volume lines changed: the vol key shows once, not per line.
	added := []string{"vol=data:/a", "vol=cfg:/b"}
	removed := []string{"vol=data:/old"}

	got := summarizeHashDiff(added, removed, 10)
	assert.Equal(t, "~ vol", got)
}

func TestHashLineKeys_NoEqualsUsesWholeLine(t *testing.T) {
	keys := hashLineKeys([]string{"plainline", "env:A=1"})
	assert.Equal(t, hashLineKeySet{"plainline", "env:A"}, keys)
}
