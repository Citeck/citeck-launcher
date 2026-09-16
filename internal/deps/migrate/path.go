package migrate

// Path is the route one migration walks. Path[0] is the image the data runs on
// now, every later element is a rung the bundle's ladder named, and the last
// is the target.
//
// It replaces the (from, to) pair every migrator used to take. The pair was
// not merely narrower — it was a different claim: it said a migration is one
// hop, and every plan built on it was free to assume so. A Path of length 2 IS
// that pair, so the ordinary single-hop case reads identically.
type Path []string

// From is what the data runs on now.
func (p Path) From() string {
	if len(p) == 0 {
		return ""
	}
	return p[0]
}

// To is where the migration ends.
func (p Path) To() string {
	if len(p) == 0 {
		return ""
	}
	return p[len(p)-1]
}

// Len is the number of images on the route, including both ends.
func (p Path) Len() int { return len(p) }

// Hops are the adjacent pairs — what the vendor is asked about, one question
// per pair.
func (p Path) Hops() [][2]string {
	if len(p) < 2 {
		return nil
	}
	out := make([][2]string, 0, len(p)-1)
	for i := 0; i+1 < len(p); i++ {
		out = append(out, [2]string{p[i], p[i+1]})
	}
	return out
}

// Rungs are what the plan CLIMBS: every element after the starting point.
func (p Path) Rungs() []string {
	if len(p) < 2 {
		return nil
	}
	return p[1:]
}
