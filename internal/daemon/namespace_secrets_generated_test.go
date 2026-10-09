package daemon

import (
	"bytes"
	"errors"
	"go/ast"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsWith builds a workspace config from ready entries.
func wsWith(defs ...bundle.SecretDefault) *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{Secrets: defs}
}

func generated(id string, length int) bundle.SecretDefault {
	return bundle.SecretDefault{ID: id, Generated: true, Length: length}
}

var alnumRe = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// storedNamespaceRows answers the rows namespace ws/ns has in the store,
// name -> value.
func storedNamespaceRows(t *testing.T, svc *storage.SecretService) map[string]string {
	t.Helper()
	metas, err := svc.ListSecrets()
	require.NoError(t, err)
	out := map[string]string{}
	for _, m := range metas {
		if !storage.IsNamespaceSecret(m) || m.Scope != namespaceSecretScope("ws", "ns") {
			continue
		}
		sec, err := svc.GetSecret(m.ID)
		require.NoError(t, err)
		out[m.Name] = sec.Value
	}
	return out
}

// A generated secret is generated ONCE, on the first seed, stored, and from
// then on it is the namespace's value: every later load and reload answers the
// same one, and neither a changed length nor turning the entry into a constant
// reaches it — the key is already in the service's data.
func TestAGeneratedSecretIsGeneratedOnceAndKept(t *testing.T) {
	svc, _ := newNsSecretService(t)

	first, err := loadNamespaceSecrets(svc, "ws", "ns", wsWith(generated("s3", 0)), nsSecretsSeed)
	require.NoError(t, err)
	value := first["s3"]
	require.Len(t, value, bundle.DefaultGeneratedSecretLength)
	assert.Regexp(t, alnumRe, value)
	assert.Equal(t, map[string]string{"s3": value}, storedNamespaceRows(t, svc), "the value is stored")

	for _, ws := range []*bundle.WorkspaceConfig{
		wsWith(generated("s3", 0)),
		wsWith(generated("s3", 64)),
		wsSecrets("s3", "a-constant-now"),
		wsSecrets(),
	} {
		got, loadErr := loadNamespaceSecrets(svc, "ws", "ns", ws, nsSecretsSeed)
		require.NoError(t, loadErr)
		assert.Equal(t, value, got["s3"], "a stored value is never generated again")
	}
	read, err := loadNamespaceSecrets(svc, "ws", "ns", wsWith(generated("s3", 0)), nsSecretsRead)
	require.NoError(t, err)
	assert.Equal(t, value, read["s3"], "reading answers the stored value")
	preview, err := loadNamespaceSecrets(svc, "ws", "ns", wsWith(generated("s3", 0)), nsSecretsPreview)
	require.NoError(t, err)
	assert.Equal(t, value, preview["s3"], "a preview answers the stored value, not the placeholder")
}

// The length is honored at both ends of the allowed range.
func TestAGeneratedSecretHasTheDeclaredLength(t *testing.T) {
	svc, _ := newNsSecretService(t)
	got, err := loadNamespaceSecrets(svc, "ws", "ns", wsWith(
		generated("short", bundle.MinGeneratedSecretLength),
		generated("long", bundle.MaxGeneratedSecretLength),
	), nsSecretsSeed)
	require.NoError(t, err)
	assert.Len(t, got["short"], bundle.MinGeneratedSecretLength)
	assert.Len(t, got["long"], bundle.MaxGeneratedSecretLength)
	assert.Regexp(t, alnumRe, got["long"])
}

// One value per NAMESPACE: two namespaces of one workspace, and the same
// namespace id in two workspaces, never share a generated key.
func TestEachNamespaceGetsItsOwnGeneratedValue(t *testing.T) {
	svc, _ := newNsSecretService(t)
	ws := wsWith(generated("s3", 0))
	a, err := loadNamespaceSecrets(svc, "ws", "a", ws, nsSecretsSeed)
	require.NoError(t, err)
	b, err := loadNamespaceSecrets(svc, "ws", "b", ws, nsSecretsSeed)
	require.NoError(t, err)
	otherWs, err := loadNamespaceSecrets(svc, "ws2", "a", ws, nsSecretsSeed)
	require.NoError(t, err)

	assert.NotEqual(t, a["s3"], b["s3"])
	assert.NotEqual(t, a["s3"], otherWs["s3"])
	assert.NotEqual(t, b["s3"], otherWs["s3"])
}

// Reading never invents a value: a generated secret the namespace does not
// have answers NOTHING (so `${secret:}` stays unresolved and whatever needs it
// is left out), and nothing is stored. A constant still answers its default,
// exactly as before generated entries existed.
func TestReadingNeverGeneratesASecret(t *testing.T) {
	svc, _ := newNsSecretService(t)
	got, err := loadNamespaceSecrets(svc, "ws", "ns", wsWith(
		generated("s3", 0),
		bundle.SecretDefault{ID: "db", Value: "observer"},
	), nsSecretsRead)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"db": "observer"}, got)
	assert.Empty(t, storedNamespaceRows(t, svc), "reading stores nothing")
}

// A reload PLAN answers the placeholder for a value the real reload will
// generate — so it shows the app as a change instead of an exclusion — and the
// placeholder never reaches the store: the seed that follows generates a real
// value, and a later preview shows that one.
func TestAPreviewAnswersAPlaceholderAndStoresNothing(t *testing.T) {
	svc, _ := newNsSecretService(t)
	ws := wsWith(generated("s3", 0))

	preview, err := loadNamespaceSecrets(svc, "ws", "ns", ws, nsSecretsPreview)
	require.NoError(t, err)
	assert.Equal(t, previewGeneratedSecret, preview["s3"])
	assert.Empty(t, storedNamespaceRows(t, svc), "a preview stores nothing")

	seeded, err := loadNamespaceSecrets(svc, "ws", "ns", ws, nsSecretsSeed)
	require.NoError(t, err)
	assert.NotEqual(t, previewGeneratedSecret, seeded["s3"])
	assert.Regexp(t, alnumRe, seeded["s3"])

	again, err := loadNamespaceSecrets(svc, "ws", "ns", ws, nsSecretsPreview)
	require.NoError(t, err)
	assert.Equal(t, seeded["s3"], again["s3"])
}

// An EMPTY stored value of a generated secret is a service running with its
// authentication switched off, so it does not count as a value: seeding
// replaces it (in the same row, with a warning), reading answers nothing and a
// preview the placeholder. A constant's empty value is still a value.
func TestAnEmptyGeneratedSecretIsReplaced(t *testing.T) {
	svc, _ := newNsSecretService(t)
	// How a namespace ends up there: the entry was a constant with an empty
	// default when the namespace first seeded it.
	_, err := loadNamespaceSecrets(svc, "ws", "ns", wsSecrets("s3", "", "keep-empty", ""), nsSecretsSeed)
	require.NoError(t, err)
	ws := wsWith(generated("s3", 0), bundle.SecretDefault{ID: "keep-empty", Value: "x"})

	read, err := loadNamespaceSecrets(svc, "ws", "ns", ws, nsSecretsRead)
	require.NoError(t, err)
	_, has := read["s3"]
	assert.False(t, has, "reading answers nothing for an empty generated value")
	assert.Empty(t, read["keep-empty"], "a constant's empty value is a value")

	preview, err := loadNamespaceSecrets(svc, "ws", "ns", ws, nsSecretsPreview)
	require.NoError(t, err)
	assert.Equal(t, previewGeneratedSecret, preview["s3"])

	logs := captureSlog(t)
	seeded, err := loadNamespaceSecrets(svc, "ws", "ns", ws, nsSecretsSeed)
	require.NoError(t, err)
	require.Len(t, seeded["s3"], bundle.DefaultGeneratedSecretLength)
	assert.Empty(t, seeded["keep-empty"])
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "empty value")

	rows := storedNamespaceRows(t, svc)
	assert.Equal(t, map[string]string{"s3": seeded["s3"], "keep-empty": ""}, rows,
		"replaced in place: still one row for the id")
}

// What is logged names the namespace and the secret and NEVER the value.
func TestAGeneratedValueIsNeverLogged(t *testing.T) {
	svc, _ := newNsSecretService(t)
	logs := captureSlog(t)
	got, err := loadNamespaceSecrets(svc, "ws", "ns", wsWith(generated("s3", 0)), nsSecretsSeed)
	require.NoError(t, err)
	out := logs.String()
	assert.Contains(t, out, "Namespace secret generated")
	assert.Contains(t, out, "secret=s3")
	assert.Contains(t, out, "ns=ns")
	assert.NotContains(t, out, got["s3"])
}

// A locked vault is UNREADABLE, never empty — including a seed that only has
// to WRITE a generated value into it. The caller then generates without the
// apps that need it and the start gate waits for the unlock; the value the
// unlock then generates is the one that stays.
func TestAGeneratedSecretFollowsTheVault(t *testing.T) {
	svc, store := newNsSecretService(t)
	require.NoError(t, svc.SetMasterPassword("pwd", false))
	locked, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.True(t, locked.IsLocked())

	ws := wsWith(generated("s3", 0))
	_, err = loadNamespaceSecrets(locked, "ws", "ns", ws, nsSecretsSeed)
	require.ErrorIs(t, err, storage.ErrSecretsLocked, "a write the locked vault cannot take is not an empty value")
	assert.Nil(t, namespaceSecretsFor(locked, "ws", "ns", ws, nsSecretsSeed))

	require.NoError(t, locked.Unlock("pwd"))
	seeded, err := loadNamespaceSecrets(locked, "ws", "ns", ws, nsSecretsSeed)
	require.NoError(t, err)
	require.NotEmpty(t, seeded["s3"])

	relocked, err := storage.NewSecretService(store)
	require.NoError(t, err)
	_, err = loadNamespaceSecrets(relocked, "ws", "ns", ws, nsSecretsRead)
	require.ErrorIs(t, err, storage.ErrSecretsLocked, "a stored value behind a locked vault is unreadable, not missing")
	require.NoError(t, relocked.Unlock("pwd"))
	again, err := loadNamespaceSecrets(relocked, "ws", "ns", ws, nsSecretsSeed)
	require.NoError(t, err)
	assert.Equal(t, seeded["s3"], again["s3"], "and it is the same value after the next unlock")
}

// Two seeds of one namespace at once must agree on ONE value: the second may
// not generate a replacement for the value the first already handed out.
func TestConcurrentSeedsAgreeOnOneValue(t *testing.T) {
	svc, _ := newNsSecretService(t)
	ws := wsWith(generated("s3", 0))
	const n = 8
	values := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			got, err := loadNamespaceSecrets(svc, "ws", "ns", ws, nsSecretsSeed)
			if err == nil {
				values[i] = got["s3"]
			}
		})
	}
	wg.Wait()
	for _, v := range values {
		assert.Equal(t, values[0], v)
	}
	assert.NotEmpty(t, values[0])
	assert.Len(t, storedNamespaceRows(t, svc), 1)
}

// The first entry for an id decides its kind, as it always decided the value:
// a later entry of the other kind neither replaces a constant's empty value
// with a generated one nor lets a reader fall back to a constant default for
// a value that is generated.
func TestTheFirstEntryForAnIDDecidesItsKind(t *testing.T) {
	svc, _ := newNsSecretService(t)
	got, err := loadNamespaceSecrets(svc, "ws", "ns", wsWith(
		bundle.SecretDefault{ID: "k", Value: ""},
		generated("k", 0),
	), nsSecretsSeed)
	require.NoError(t, err)
	assert.Empty(t, got["k"], "the constant came first; its empty value is a value")

	read, err := loadNamespaceSecrets(svc, "ws", "other", wsWith(
		generated("g", 0),
		bundle.SecretDefault{ID: "g", Value: "a-constant"},
	), nsSecretsRead)
	require.NoError(t, err)
	_, has := read["g"]
	assert.False(t, has, "the generated entry came first; reading invents nothing")
}

// The value is drawn UNIFORMLY: a byte at or above 248 (= 4×62) is discarded,
// never folded onto the alphabet modulo 62 — which would make "A".."H" a
// quarter likelier than every other character.
func TestGeneratedValuesAreUnbiased(t *testing.T) {
	// 248..255 must all be skipped; 61 is '9', 0 is 'A', 247 is 247%62 = 61 → '9'.
	src := bytes.NewReader(append([]byte{248, 249, 250, 251, 252, 253, 254, 255, 61, 0, 247},
		make([]byte, 64)...))
	got, err := generateSecretValueFrom(src, 3)
	require.NoError(t, err)
	assert.Equal(t, "9A9", got)

	// Two full cycles of every byte value: with the discard, every character
	// comes out exactly 8 times; folding 248..255 in would give "A".."H" 9.
	counts := map[byte]int{}
	stream := make([]byte, 0, 2*256)
	for range 2 {
		for i := range 256 {
			stream = append(stream, byte(i))
		}
	}
	value, err := generateSecretValueFrom(bytes.NewReader(append(stream, make([]byte, 512)...)), 2*248)
	require.NoError(t, err)
	for i := range len(value) {
		counts[value[i]]++
	}
	require.Len(t, counts, len(secretValueAlphabet))
	for c, n := range counts {
		assert.Equal(t, 8, n, "character %q", c)
	}
}

func TestAFailingRandomSourceIsAnError(t *testing.T) {
	_, err := generateSecretValueFrom(strings.NewReader(""), 16)
	require.Error(t, err)
	_, err = generateSecretValueFrom(errReader{}, 16)
	require.Error(t, err)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

// Which mode each caller loads secrets in is the whole rule, so it is pinned
// at the call sites: the two generations whose containers start SEED (or a
// generated key would never exist), the reload plan PREVIEWs (or it would
// store, or report the app excluded), and everything that acts on values
// outside a generation only READS (or it would invent a key no container
// has).
func TestEverySecretLoadNamesItsMode(t *testing.T) {
	cases := []struct {
		file, fn, call, mode string
	}{
		{"namespace_loader.go", "loadNamespace", "namespaceSecretsFor", "nsSecretsSeed"},
		{"server.go", "doReloadEx", "namespaceSecretsFor", "nsSecretsSeed"},
		{"routes_reloadplan.go", "resolveReloadPlanInputs", "namespaceSecretsFor", "nsSecretsPreview"},
		{"deps_env.go", "GenerateDefForVolume", "namespaceSecretsFor", "nsSecretsRead"},
	}
	for _, c := range cases {
		t.Run(c.fn, func(t *testing.T) {
			fn := parseFuncDecl(t, c.file, c.fn)
			var modes []string
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == c.call && len(call.Args) > 0 {
					if arg, ok := call.Args[len(call.Args)-1].(*ast.Ident); ok {
						modes = append(modes, arg.Name)
					} else {
						modes = append(modes, "<not an identifier>")
					}
				}
				return true
			})
			assert.Equal(t, []string{c.mode}, modes, "%s must call %s exactly once, in %s", c.fn, c.call, c.mode)
		})
	}
}
