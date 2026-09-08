package deps

import (
	"strconv"
	"strings"
)

// Version is the numeric part of an image tag. Raw keeps the full tag so a
// message can name it ("4.2.9-management"), Major/Minor/Patch drive the rules.
type Version struct {
	Major, Minor, Patch int
	Raw                 string
}

// String renders the numeric part only ("17.5", "18", "4.2.9").
func (v Version) String() string {
	s := strconv.Itoa(v.Major)
	if v.Minor > 0 || v.Patch > 0 {
		s += "." + strconv.Itoa(v.Minor)
	}
	if v.Patch > 0 {
		s += "." + strconv.Itoa(v.Patch)
	}
	return s
}

// ParseImageVersion reads the leading N[.N[.N]] of an image tag. The tag is
// what follows the LAST ':' after the last '/' — a registry port
// ("host:5000/repo:17") must not be mistaken for a tag. Anything without a
// leading number ("latest", a digest, a custom name) is unknown: ok=false.
// Unknown is the safe answer — the caller treats it as a breaking change.
//
// A digest reference is unknown even when it carries a readable tag
// ("postgres:17@sha256:…"): the digest is what Docker resolves, the tag beside
// it is a label anyone can move, so believing it would pin the version off a
// string that does not decide what runs. The consequence is worth stating
// plainly — a namespace whose dependency is pinned by digest is held back
// PERMANENTLY (deps.Breaking answers true for every candidate) and the only
// way out is an edit that gives it a readable tag.
func ParseImageVersion(image string) (Version, bool) {
	if image == "" {
		return Version{}, false
	}
	if strings.Contains(image, "@") {
		return Version{}, false // digest reference: no tag semantics
	}
	name := image
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	i := strings.LastIndex(name, ":")
	if i < 0 || i == len(name)-1 {
		return Version{}, false
	}
	tag := name[i+1:]
	v := Version{Raw: tag}
	// numeric prefix: digits and dots, stop at the first other rune
	end := 0
	for end < len(tag) && (tag[end] == '.' || (tag[end] >= '0' && tag[end] <= '9')) {
		end++
	}
	// strings.Split never answers an empty slice, so parts[0] is the whole
	// test: it is "" for a tag with no leading digit ("latest", "alpine").
	parts := strings.Split(strings.TrimSuffix(tag[:end], "."), ".")
	if parts[0] == "" {
		return Version{}, false
	}
	nums := make([]int, 0, 3)
	for k, p := range parts {
		if k == 3 {
			break
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, false
		}
		nums = append(nums, n)
	}
	v.Major = nums[0]
	if len(nums) > 1 {
		v.Minor = nums[1]
	}
	if len(nums) > 2 {
		v.Patch = nums[2]
	}
	return v, true
}

// SplitImageRef splits a tagged image reference into its repository and tag,
// by the same rule ParseImageVersion uses to find the tag (the last ':' after
// the last '/', so a registry port is never mistaken for one).
//
// ok=false for a digest reference or a reference with no tag: neither has a
// repository that can be swapped without changing WHICH image runs, and that
// is the only thing callers use this for.
func SplitImageRef(image string) (repo, tag string, ok bool) {
	if image == "" || strings.Contains(image, "@") {
		return "", "", false
	}
	name := image
	prefix := ""
	if i := strings.LastIndex(name, "/"); i >= 0 {
		prefix, name = name[:i+1], name[i+1:]
	}
	i := strings.LastIndex(name, ":")
	if i <= 0 || i == len(name)-1 {
		return "", "", false
	}
	return prefix + name[:i], name[i+1:], true
}
