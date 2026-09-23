package namespace

import (
	"log/slog"
	"regexp"
	"slices"
	"sort"
)

// secretRefPattern matches `${secret:<id>}`. An id is what a workspace
// `secrets:` entry names: letters, digits, dot, dash and underscore.
var secretRefPattern = regexp.MustCompile(`\$\{secret:([A-Za-z0-9._-]+)\}`)

// resolveSecretRefs substitutes every `${secret:<id>}` the namespace has a
// value for, and LEAVES IN PLACE every one it has not — so the reference is
// still there for excludeAppsMissingSecrets to find. An empty value is a value
// (the operator wrote it on purpose) and is substituted.
func resolveSecretRefs(s string, secrets map[string]string) string {
	return secretRefPattern.ReplaceAllStringFunc(s, func(ref string) string {
		id := secretRefPattern.FindStringSubmatch(ref)[1]
		if v, ok := secrets[id]; ok {
			return v
		}
		return ref
	})
}

// excludeAppsMissingSecrets removes every app that still carries an unresolved
// `${secret:<id>}` after all generators have run. A service whose secret did
// not arrive is not started: running a database with an empty password is how
// it gets initialized with one, and a client with an empty password fails in a
// way that says nothing about why. Whatever depends on the app is then removed
// by pruneAppsWithMissingDeps, which runs right after this.
//
// The rest of the namespace is generated as usual — a missing secret for one
// declared service must not take the stand down.
//
// Must run after every generator and before pruneAppsWithMissingDeps.
func excludeAppsMissingSecrets(ctx *NsGenContext) {
	for name, app := range ctx.Applications {
		missing := unresolvedSecretRefs(app, ctx.CloudConfig[name])
		if len(missing) == 0 {
			continue
		}
		slog.Error("App excluded from the namespace: a secret it references has no value in this namespace",
			"app", name, "secrets", missing)
		delete(ctx.Applications, name)
		delete(ctx.CloudConfig, name)
	}
}

// unresolvedSecretRefs lists the secret ids still referenced anywhere in the
// app's definition or its cloud config, sorted and without duplicates.
func unresolvedSecretRefs(app *AppBuilder, cloudCfg map[string]any) []string {
	var ids []string
	collect := func(s string) {
		for _, m := range secretRefPattern.FindAllStringSubmatch(s, -1) {
			ids = append(ids, m[1])
		}
	}
	for _, e := range app.Environments {
		collect(e.Value)
	}
	for _, c := range app.Cmd {
		collect(c)
	}
	for _, ic := range app.InitContainers {
		for _, e := range ic.Environments {
			collect(e.Value)
		}
		for _, c := range ic.Cmd {
			collect(c)
		}
	}
	for _, a := range app.InitActions {
		for _, c := range a.Exec {
			collect(c)
		}
	}
	walkStrings(cloudCfg, collect)
	sort.Strings(ids)
	return slices.Compact(ids)
}

// walkStrings calls fn for every string inside a cloud-config value.
func walkStrings(v any, fn func(string)) {
	switch t := v.(type) {
	case string:
		fn(t)
	case map[string]any:
		for _, x := range t {
			walkStrings(x, fn)
		}
	case []any:
		for _, x := range t {
			walkStrings(x, fn)
		}
	}
}
