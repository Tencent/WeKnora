package manifest

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// comparator is one term of an engines range, such as ">=0.10.0".
type comparator struct {
	op      string
	version string // canonical, with the "v" prefix semver wants
}

// parseRange reads a space-separated list of comparators, all of which must
// hold: ">=0.10.0 <1.0.0". A bare version means "=".
func parseRange(r string) ([]comparator, error) {
	var out []comparator
	for _, term := range strings.Fields(r) {
		op := "="
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(term, candidate) {
				op, term = candidate, strings.TrimPrefix(term, candidate)
				break
			}
		}
		if !isStrictSemver(term) {
			return nil, fmt.Errorf("%q is not a semantic version", term)
		}
		out = append(out, comparator{op: op, version: "v" + term})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("range is empty")
	}
	return out, nil
}

// CheckEngines reports whether host (the running WeKnora version) satisfies
// the manifest's engines.weknora range. Development builds without a release
// version pass, as does a manifest without a range.
func (m *Manifest) CheckEngines(host string) error {
	if m.Engines.WeKnora == "" {
		return nil
	}
	v := "v" + strings.TrimPrefix(strings.TrimSpace(host), "v")
	if !semver.IsValid(v) {
		return nil
	}
	terms, err := parseRange(m.Engines.WeKnora)
	if err != nil {
		return fmt.Errorf("engines.weknora: %w", err)
	}
	for _, c := range terms {
		cmp := semver.Compare(v, c.version)
		ok := map[string]bool{">=": cmp >= 0, "<=": cmp <= 0, ">": cmp > 0, "<": cmp < 0, "=": cmp == 0}[c.op]
		if !ok {
			return fmt.Errorf("plugin needs WeKnora %s, this is %s", m.Engines.WeKnora, strings.TrimPrefix(v, "v"))
		}
	}
	return nil
}
