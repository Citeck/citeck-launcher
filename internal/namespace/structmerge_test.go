package namespace

import (
	"encoding/json"
	"reflect"
	"testing"
)

func tree(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad json %q: %v", s, err)
	}
	return v
}

func TestDiffApplyRoundTrip(t *testing.T) {
	base := tree(t, `{"image":"img:1","env":{"A":"1","B":"2"},"ports":["80","443"]}`)
	target := tree(t, `{"image":"img:1","env":{"A":"9","B":"2","C":"3"},"ports":["80"]}`)
	patch := DiffTree(base, target)
	wantPatch := tree(t, `{"env":{"A":"9","C":"3"},"ports":["80"]}`)
	if !reflect.DeepEqual(patch, wantPatch) {
		t.Fatalf("patch = %v, want %v", patch, wantPatch)
	}
	got := ApplyTree(base, patch)
	if !reflect.DeepEqual(got, target) {
		t.Fatalf("apply(base,patch) = %v, want %v", got, target)
	}
}

func TestDiffEqualReturnsNil(t *testing.T) {
	base := tree(t, `{"a":1,"b":{"c":2}}`)
	if p := DiffTree(base, tree(t, `{"a":1,"b":{"c":2}}`)); p != nil {
		t.Fatalf("expected nil patch for equal trees, got %v", p)
	}
}

func TestApplyDeletesKeyOnNull(t *testing.T) {
	base := tree(t, `{"env":{"A":"1","B":"2"}}`)
	patch := tree(t, `{"env":{"B":null}}`)
	got := ApplyTree(base, patch)
	want := tree(t, `{"env":{"A":"1"}}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDiffRemovedKeyBecomesNull(t *testing.T) {
	base := tree(t, `{"env":{"A":"1","B":"2"}}`)
	target := tree(t, `{"env":{"A":"1"}}`)
	patch := DiffTree(base, target)
	want := tree(t, `{"env":{"B":null}}`)
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch = %v, want %v", patch, want)
	}
}

func TestArraysAreAtomic(t *testing.T) {
	base := tree(t, `{"cmd":["a","b","c"]}`)
	target := tree(t, `{"cmd":["a","x"]}`)
	patch := DiffTree(base, target)
	want := tree(t, `{"cmd":["a","x"]}`)
	if !reflect.DeepEqual(patch, want) {
		t.Fatalf("patch = %v, want %v", patch, want)
	}
}
