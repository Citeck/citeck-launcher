package deps

// UpgradeRoute turns a pin and a LADDER of images into the route a migration
// walks: the pin first, then every rung strictly newer than it, in the order
// the author wrote them.
//
// Three rules, and each of them is a refusal rather than a repair:
//
//   - a rung nobody can parse invalidates the WHOLE ladder. "Strictly newer
//     than the pin" has no answer for it, and dropping it would produce a hop
//     across the gap it left — exactly the jump the ladder exists to forbid;
//   - a ladder whose rungs do not ascend is not a route. Sorting it would be
//     the launcher rewriting the author's statement about their own vendor;
//   - a pin nobody can parse has no place on the ladder at all.
//
// A ladder of one image answers the ordinary pair, so nothing downstream needs
// a second shape for the single-hop case. A pin already at the top answers a
// route of length 1, which every caller reads as "nothing to do".
func UpgradeRoute(d Descriptor, pinned string, ladder []string) ([]string, bool) {
	if len(ladder) == 0 {
		return nil, false
	}
	pinV, ok := d.ParseVersion(pinned)
	if !ok {
		return nil, false
	}
	route := []string{pinned}
	prev := pinV
	var ascending Version
	var haveAscending bool
	for _, image := range ladder {
		v, ok := d.ParseVersion(image)
		if !ok {
			return nil, false
		}
		if haveAscending && compareForRoute(ascending, v) >= 0 {
			return nil, false
		}
		ascending, haveAscending = v, true
		// Rungs at or below the pin are behind the operator; the route starts
		// where they are standing.
		if compareForRoute(v, prev) <= 0 {
			continue
		}
		route = append(route, image)
		prev = v
	}
	return route, true
}

// compareForRoute orders two versions by (major, minor, patch), returning >0
// when a is newer than b, <0 when a is older, 0 when equal — the same
// convention compareVersions uses. It is expressed over MovesBackwards so the
// ordering rule stays in ONE place: a second hand-written comparison here is
// how "4.10 is older than 4.9" gets reintroduced.
//
// MovesBackwards(from, to) reports whether to is older than from — confirmed
// against its doc comment and by direct experiment: MovesBackwards(x, y) is
// true exactly when x is newer than y. So MovesBackwards(a, b) answers "is a
// newer than b" directly, and MovesBackwards(b, a) answers "is a older than
// b" — that pairing, not its mirror image, is what must feed the two cases
// below.
func compareForRoute(a, b Version) int {
	switch {
	case MovesBackwards(a, b): // a is newer than b
		return 1
	case MovesBackwards(b, a): // a is older than b
		return -1
	default:
		return 0
	}
}
