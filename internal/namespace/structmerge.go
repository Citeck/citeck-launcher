package namespace

// structmerge.go — generic JSON-merge-patch (RFC 7386 semantics) over decoded
// data trees (map[string]any / []any / scalars). Used by appdefpatch.go (apply)
// and filemerge.go (compute the file delta). Semantics: objects merge key-by-
// key; arrays are ATOMIC; a key present in base but absent in target becomes an
// explicit null in the patch; ApplyTree treats null as "delete this key".

// DiffTree returns a sparse patch p such that ApplyTree(base, p) deep-equals
// target. Returns nil when base and target are already equal.
func DiffTree(base, target any) any {
	baseObj, baseIsObj := base.(map[string]any)
	targetObj, targetIsObj := target.(map[string]any)
	if !baseIsObj || !targetIsObj {
		if treeEqual(base, target) {
			return nil
		}
		return target
	}
	patch := map[string]any{}
	for k, tv := range targetObj {
		bv, ok := baseObj[k]
		if !ok {
			patch[k] = tv
			continue
		}
		if sub := DiffTree(bv, tv); sub != nil {
			patch[k] = sub
		}
	}
	for k := range baseObj {
		if _, ok := targetObj[k]; !ok {
			patch[k] = nil
		}
	}
	if len(patch) == 0 {
		return nil
	}
	return patch
}

// ApplyTree overlays patch onto base and returns the result. base is not
// mutated. A null patch value deletes the key; a non-object patch replaces
// base wholesale (atomic arrays/scalars).
func ApplyTree(base, patch any) any {
	patchObj, patchIsObj := patch.(map[string]any)
	if !patchIsObj {
		return patch
	}
	baseObj, baseIsObj := base.(map[string]any)
	out := map[string]any{}
	if baseIsObj {
		for k, v := range baseObj {
			out[k] = v
		}
	}
	for k, pv := range patchObj {
		if pv == nil {
			delete(out, k)
			continue
		}
		out[k] = ApplyTree(out[k], pv)
	}
	return out
}

func treeEqual(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !treeEqual(v, bv[k]) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !treeEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
