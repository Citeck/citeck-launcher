package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Drift gate between the Go wire DTOs (dto.go) and the hand-written
// TypeScript interfaces the Web UI consumes (web/src/lib/types.ts plus the
// DTO interfaces declared inline in web/src/lib/api.ts).
//
// The TS side is deliberately NOT generated (tygo or similar): the
// interfaces carry UI-specific refinements a generator would flatten —
// doc comments, literal unions (DiagnosticsStatus, 'desktop' | 'server'),
// `| null` on slices/maps the daemon may omit — and a few interfaces are
// renamed or come from other Go packages. Instead this test pins the part
// that actually breaks at runtime: the JSON field names. For every DTO
// struct in dto.go that has a matching `export interface` on the web side,
// each Go json tag must be declared as a property of that interface and
// vice versa, so a field rename on either side fails the build.

// webSources are the TS files holding DTO interfaces, relative to this package.
var webSources = []string{
	filepath.Join("..", "..", "web", "src", "lib", "types.ts"),
	filepath.Join("..", "..", "web", "src", "lib", "api.ts"),
}

// knownFieldGaps lists "Struct.jsonField" pairs intentionally absent from the
// matching web interface. Every entry must say why and when to remove it.
var knownFieldGaps = map[string]string{
	// (no gaps — LinkDto.alwaysEnabled now declared on the web interface)
}

func TestWebTypesMatchGoDTOs(t *testing.T) {
	goStructs := parseDTOStructs(t, "dto.go")
	tsIfaces := map[string]map[string]bool{}
	for _, src := range webSources {
		data, err := os.ReadFile(src)
		require.NoError(t, err, "web UI source must be present for the DTO drift gate")
		maps.Copy(tsIfaces, parseTSInterfaces(string(data)))
	}
	require.NotEmpty(t, goStructs)
	require.NotEmpty(t, tsIfaces)

	matched := 0
	for structName, fields := range goStructs {
		props, ok := tsIfaces[structName]
		if !ok {
			// e.g. OpenDirResponseDto is declared as OpenDirResponse in api.ts.
			props, ok = tsIfaces[strings.TrimSuffix(structName, "Dto")]
		}
		if !ok {
			// DTO not consumed by the web UI (CLI/daemon-only) — nothing to pin.
			t.Logf("note: %s has no web interface (CLI/daemon-only DTO)", structName)
			continue
		}
		matched++

		for _, f := range fields {
			key := structName + "." + f
			if _, allowed := knownFieldGaps[key]; allowed {
				if props[f] {
					t.Errorf("%s is in knownFieldGaps but the web interface declares it — remove the stale entry", key)
				}
				continue
			}
			if !props[f] {
				t.Errorf("Go field %s (json %q) is missing from the web interface — types.ts/api.ts out of sync with dto.go", key, f)
			}
		}

		goFields := map[string]bool{}
		for _, f := range fields {
			goFields[f] = true
		}
		for p := range props {
			if !goFields[p] {
				t.Errorf("web interface %s declares %q which is not a json field of the Go struct — stale or renamed on the Go side", structName, p)
			}
		}
	}
	require.NotZero(t, matched, "no Go DTO matched any web interface — matching logic broken")
}

// parseDTOStructs returns struct name -> json field names for every struct
// type declared in the given file of this package.
func parseDTOStructs(t *testing.T, file string) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	res := map[string][]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		var fields []string
		for _, fld := range st.Fields.List {
			if fld.Tag == nil {
				continue
			}
			tag := reflect.StructTag(strings.Trim(fld.Tag.Value, "`"))
			name, _, _ := strings.Cut(tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			fields = append(fields, name)
		}
		res[ts.Name.Name] = fields
		return true
	})
	return res
}

var (
	tsBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	tsLineComment  = regexp.MustCompile(`(?m)//.*$`)
	tsIfaceDecl    = regexp.MustCompile(`(?m)^export interface ([A-Za-z0-9_]+)\s*\{`)
	tsProp         = regexp.MustCompile(`(?m)^\s*([A-Za-z_$][A-Za-z0-9_$]*)\??:`)
)

// parseTSInterfaces extracts `export interface Name { ... }` declarations and
// their property names. Comments are stripped first; DTO interface bodies are
// flat property lists, so a brace-depth scan finds the body end.
func parseTSInterfaces(src string) map[string]map[string]bool {
	src = tsLineComment.ReplaceAllString(tsBlockComment.ReplaceAllString(src, ""), "")
	res := map[string]map[string]bool{}
	for _, m := range tsIfaceDecl.FindAllStringSubmatchIndex(src, -1) {
		name := src[m[2]:m[3]]
		body := braceBody(src[m[1]:]) // m[1] is just past the opening '{'
		props := map[string]bool{}
		for _, pm := range tsProp.FindAllStringSubmatch(body, -1) {
			props[pm[1]] = true
		}
		res[name] = props
	}
	return res
}

// braceBody returns the prefix of s up to the '}' matching an already-open
// brace (depth starts at 1).
func braceBody(s string) string {
	depth := 1
	for i, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}
