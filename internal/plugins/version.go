package plugins

import (
	"fmt"
	"strconv"
	"strings"
)

// ver is a parsed dotted version (major.minor.patch); missing parts are 0.
type ver struct{ major, minor, patch int }

// parseVer parses "27.1.1", "1.26", "v0.114.0" etc. Leading 'v' and any
// pre-release/build suffix after the patch are ignored.
func parseVer(s string) (ver, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	// drop a -prerelease or +build suffix
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.SplitN(s, ".", 3)
	var v ver
	dst := []*int{&v.major, &v.minor, &v.patch}
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return v, fmt.Errorf("invalid version %q: %w", s, err)
		}
		*dst[i] = n
	}
	return v, nil
}

// cmp returns -1, 0, +1 comparing a to b.
func (a ver) cmp(b ver) int {
	for _, d := range [][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if d[0] < d[1] {
			return -1
		}
		if d[0] > d[1] {
			return 1
		}
	}
	return 0
}

// matchRange reports whether version `s` satisfies a space-separated set of
// simple constraints like ">=24.0", ">=20.0 <24.0", "<20.0". Each token is an
// operator (>=, >, <=, <, =) followed by a version; ALL must hold (AND). This is
// deliberately not a full semver solver — simple comparison is enough for CLIs.
func matchRange(version, constraints string) (bool, error) {
	v, err := parseVer(version)
	if err != nil {
		return false, err
	}
	for _, tok := range strings.Fields(constraints) {
		op, rest := splitOp(tok)
		bound, err := parseVer(rest)
		if err != nil {
			return false, fmt.Errorf("bad constraint %q: %w", tok, err)
		}
		c := v.cmp(bound)
		ok := false
		switch op {
		case ">=":
			ok = c >= 0
		case ">":
			ok = c > 0
		case "<=":
			ok = c <= 0
		case "<":
			ok = c < 0
		case "=", "==":
			ok = c == 0
		default:
			return false, fmt.Errorf("unknown operator in constraint %q", tok)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// splitOp separates a leading comparison operator from the version in a token.
func splitOp(tok string) (op, rest string) {
	for _, o := range []string{">=", "<=", "==", ">", "<", "="} {
		if strings.HasPrefix(tok, o) {
			return o, tok[len(o):]
		}
	}
	return "=", tok // bare version → exact match
}
