package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

// decodeOne parses a one-key document and hands the value node to the decoder,
// which is how every real call site reaches it.
func decodeOne(t *testing.T, doc string) []string {
	t.Helper()
	var root yaml.Node
	err := yaml.Unmarshal([]byte(doc), &root)
	assert.NoError(t, err) //nolint:testifylint // deliberate: this helper's failure mode is a clear test failure either way
	// root is a document node; its content[0] is the mapping, whose content[1]
	// is the value of the single key.
	return decodeImageValues(root.Content[0].Content[1])
}

func TestImageValuesAcceptsAScalar(t *testing.T) {
	assert.Equal(t, []string{"postgres:18.6"}, decodeOne(t, "image: postgres:18.6\n"))
}

func TestImageValuesAcceptsARepositoryTagMap(t *testing.T) {
	assert.Equal(t, []string{"postgres:18.6"},
		decodeOne(t, "image:\n  repository: postgres\n  tag: \"18.6\"\n"))
}

// The tag must keep its RAW TEXT: an unquoted 17.10 read through a generic
// map becomes float64(17.1) and the entry then names a version nobody wrote.
func TestImageValuesKeepsATrailingZeroInTheTag(t *testing.T) {
	assert.Equal(t, []string{"postgres:17.10"},
		decodeOne(t, "image:\n  repository: postgres\n  tag: 17.10\n"))
}

func TestImageValuesAcceptsASequenceOfScalars(t *testing.T) {
	assert.Equal(t, []string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.19.1"},
		decodeOne(t, "image:\n  - qdrant/qdrant:v1.15.5\n  - qdrant/qdrant:v1.19.1\n"))
}

func TestImageValuesAcceptsASequenceOfMaps(t *testing.T) {
	doc := "image:\n" +
		"  - {repository: qdrant/qdrant, tag: v1.15.5}\n" +
		"  - {repository: qdrant/qdrant, tag: v1.19.1}\n"
	assert.Equal(t, []string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.19.1"}, decodeOne(t, doc))
}

func TestImageValuesAcceptsAMixedSequence(t *testing.T) {
	doc := "image:\n" +
		"  - qdrant/qdrant:v1.15.5\n" +
		"  - {repository: qdrant/qdrant, tag: v1.19.1}\n"
	assert.Equal(t, []string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.19.1"}, decodeOne(t, doc))
}

// A rung nobody can read poisons the WHOLE ladder. Dropping just that rung
// would silently produce a hop the vendor was never asked about.
func TestOneUnreadableRungInvalidatesTheWholeLadder(t *testing.T) {
	doc := "image:\n" +
		"  - qdrant/qdrant:v1.15.5\n" +
		"  - {repository: qdrant/qdrant}\n" +
		"  - qdrant/qdrant:v1.19.1\n"
	assert.Nil(t, decodeOne(t, doc))
}

func TestImageValuesReadsNothingOutOfAnEmptySequence(t *testing.T) {
	assert.Nil(t, decodeOne(t, "image: []\n"))
}

func TestImageValuesReadsNothingOutOfAShapeItDoesNotKnow(t *testing.T) {
	assert.Nil(t, decodeOne(t, "image:\n  nested:\n    deeper: 1\n"))
}
