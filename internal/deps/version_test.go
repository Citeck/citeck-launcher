package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseImageVersion(t *testing.T) {
	cases := []struct {
		image string
		want  Version
		ok    bool
	}{
		{"postgres:17.5", Version{Major: 17, Minor: 5, Raw: "17.5"}, true},
		{"postgres:17", Version{Major: 17, Raw: "17"}, true},
		{"postgres:18.1-alpine", Version{Major: 18, Minor: 1, Raw: "18.1-alpine"}, true},
		{"rabbitmq:4.2.9-management", Version{Major: 4, Minor: 2, Patch: 9, Raw: "4.2.9-management"}, true},
		{"nexus.citeck.ru:5000/infra/postgres:17.11", Version{Major: 17, Minor: 11, Raw: "17.11"}, true},
		{"keycloak/keycloak:26.4.5", Version{Major: 26, Minor: 4, Patch: 5, Raw: "26.4.5"}, true},
		// A fourth component is ignored rather than making the tag unknown.
		{"someimage:1.2.3.4", Version{Major: 1, Minor: 2, Patch: 3, Raw: "1.2.3.4"}, true},
		{"postgres:latest", Version{}, false},
		{"postgres", Version{}, false},
		{"postgres@sha256:abcdef", Version{}, false},
		// A digest reference is unknown even when it carries a readable tag:
		// the digest is what Docker resolves and the tag beside it is a label
		// anyone can move, so believing it would pin the version off a string
		// that does not decide what runs. The documented consequence is a
		// PERMANENT hold (deps.Breaking answers true for every candidate), and
		// the only way out is an edit that gives the image a plain tag.
		{"postgres:17@sha256:abcdef", Version{}, false},
		{"nexus.citeck.ru:5000/infra/postgres:17.11@sha256:abcdef", Version{}, false},
		{"", Version{}, false},
		{"postgres:", Version{}, false},
		{"postgres:1..2", Version{}, false},
		// A registry port is not a tag: this image is untagged.
		{"nexus.citeck.ru:5000/infra/postgres", Version{}, false},
	}
	for _, c := range cases {
		t.Run(c.image, func(t *testing.T) {
			got, ok := ParseImageVersion(c.image)
			require.Equal(t, c.ok, ok)
			if ok {
				assert.Equal(t, c.want, got)
			}
		})
	}
}

func TestVersionString(t *testing.T) {
	cases := []struct {
		v    Version
		want string
	}{
		{Version{Major: 17, Raw: "17"}, "17"},
		{Version{Major: 17, Minor: 5, Raw: "17.5"}, "17.5"},
		{Version{Major: 4, Minor: 2, Patch: 9, Raw: "4.2.9-management"}, "4.2.9"},
		// A zero minor is still rendered when there is a patch, so the numeric
		// part never reads as "18.3" for 18.0.3.
		{Version{Major: 18, Patch: 3}, "18.0.3"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.v.String())
	}
}
