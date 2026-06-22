package plugins

import (
	"fmt"
	"regexp"
)

// Setup describes how a tool is installed/verified (the `setup:` block).
// Exactly one of Asdf / Unmanaged is the install method:
//   - Asdf: the asdf plugin name (version comes from the manager's version_source).
//   - Unmanaged: a literal install command (for tools with no manager plugin);
//     Version pins the expected version (since it isn't in .tool-versions).
type Setup struct {
	Asdf      string `yaml:"asdf,omitempty"`
	Unmanaged string `yaml:"unmanaged,omitempty"`
	Version   string `yaml:"version,omitempty"` // expected version for unmanaged tools
	Dir       string `yaml:"dir,omitempty"`     // run detect (+ read the pin) from this subdir
}

// Manager is a tools-manager manifest (managers/*.yaml) — how a version manager
// (asdf, mise, …) installs/verifies tools.
type Manager struct {
	Manager       string `yaml:"manager"`
	Detect        string `yaml:"detect"`
	VersionSource string `yaml:"version_source"` // e.g. "tool-versions"
	Ops           struct {
		Pinned  string `yaml:"pinned"`  // read the pinned version for a plugin
		Install string `yaml:"install"` // install a plugin at a version
	} `yaml:"ops"`
}

// semverToken matches the first X.Y or X.Y.Z (optionally v-prefixed) in a string.
var semverToken = regexp.MustCompile(`v?(\d+\.\d+(?:\.\d+)?)`)

// ExtractVersion pulls a version out of a tool's `detect` output. If `pattern`
// is set (a regex with one capture group), it's used; otherwise the first
// semver-looking token is taken. Returns the captured version string (no leading
// v) or an error if none is found.
func ExtractVersion(out, pattern string) (string, error) {
	if pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("bad version_pattern %q: %w", pattern, err)
		}
		m := re.FindStringSubmatch(out)
		if len(m) < 2 {
			return "", fmt.Errorf("version_pattern %q matched no version in output", pattern)
		}
		return m[1], nil
	}
	m := semverToken.FindStringSubmatch(out)
	if len(m) < 2 {
		return "", fmt.Errorf("no version found in output")
	}
	return m[1], nil
}

// SameVersion reports whether two version strings are the same release, comparing
// loosely (leading v stripped, missing patch treated as 0). e.g. "2.12" == "2.12.0".
func SameVersion(a, b string) bool {
	va, ea := parseVer(a)
	vb, eb := parseVer(b)
	if ea != nil || eb != nil {
		return a == b // fall back to string equality if either won't parse
	}
	return va.cmp(vb) == 0
}
