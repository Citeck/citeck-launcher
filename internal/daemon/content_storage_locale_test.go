package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/i18n"
)

// The content-storage sentences the daemon builds (the write gate's refusals,
// the provisioner's errors) live in the CLI locale asset and are rendered at
// the HTTP boundary. Nothing at run time notices a key that is missing from a
// locale or a placeholder the code never fills: Render falls back to the key
// or leaves `{error}` in the sentence, and the wire shape is unchanged either
// way. So this test reads the daemon's own source, finds every call that
// names a `contentStorage.*` key, and holds the eight locale files to it.

// csMessageCall is one call site that names a content-storage key.
type csMessageCall struct {
	key  string
	args []string // placeholder names the call fills
	pos  string
}

// contentStorageMessageCalls parses the package's non-test sources and
// returns every call whose first argument is a `contentStorage.*` string
// literal (msg.New, Translator.T), with the placeholder names it passes —
// the string literals at the odd argument positions.
func contentStorageMessageCalls(t *testing.T) []csMessageCall {
	t.Helper()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	files = append(files, "../namespace/content_storage_policy.go")
	fset := token.NewFileSet()
	var calls []csMessageCall
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(name) //nolint:gosec // G304: a file of this package, listed by Glob
		require.NoError(t, readErr)
		if !strings.Contains(string(src), `"contentStorage.`) {
			continue
		}
		f, parseErr := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, parseErr)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			key, ok := stringLit(call.Args[0])
			if !ok || !strings.HasPrefix(key, "contentStorage.") {
				return true
			}
			c := csMessageCall{key: key, pos: fset.Position(call.Pos()).String()}
			for i := 1; i < len(call.Args); i += 2 {
				arg, isLit := stringLit(call.Args[i])
				require.Truef(t, isLit, "%s: placeholder name #%d of %s is not a string literal", c.pos, i, key)
				c.args = append(c.args, arg)
			}
			calls = append(calls, c)
			return true
		})
	}
	return calls
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

var csPlaceholderRe = regexp.MustCompile(`\{([A-Za-z]+)\}`)

func placeholdersOf(s string) []string {
	var out []string
	for _, m := range csPlaceholderRe.FindAllStringSubmatch(s, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

func TestContentStorageMessagesExistInEveryLocaleWithTheirPlaceholders(t *testing.T) {
	calls := contentStorageMessageCalls(t)
	// The scan itself must be seeing the code: the write gate's two refusals
	// and the provisioner's errors are all built in this package.
	keys := make([]string, 0, len(calls))
	for _, c := range calls {
		keys = append(keys, c.key)
	}
	for _, want := range []string{
		"contentStorage.msg.downgrade",
		"contentStorage.msg.unavailable.noProviderImage",
		"contentStorage.msg.notS3",
		"contentStorage.msg.provision.setDefaultFailed",
		"contentStorage.msg.provision.verifyFailed",
	} {
		require.Containsf(t, keys, want, "the source scan no longer finds %s", want)
	}

	for _, loc := range i18n.SupportedLocales {
		table := i18n.LoadLocale(loc.Code)
		require.NotNilf(t, table, "locale %s does not load", loc.Code)
		for _, c := range calls {
			text, ok := table[c.key]
			if !assert.Truef(t, ok, "%s: key %s is missing from %s.json", c.pos, c.key, loc.Code) {
				continue
			}
			want := slices.Clone(c.args)
			slices.Sort(want)
			assert.Equalf(t, want, placeholdersOf(text),
				"%s: %s.json %s has placeholders that differ from what the code fills", c.pos, loc.Code, c.key)
		}
	}
}
