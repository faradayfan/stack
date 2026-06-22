// Package plugins loads declarative tool manifests (the "plugins") and selects
// the command template for an abstract step, honoring the installed tool version.
// See docs/PLUGIN-MODEL.md.
package plugins

import (
	"embed"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed manifests/*.yaml
var builtin embed.FS

// Step is the command template + (for documentation) the inputs it consumes.
type Step struct {
	Command string `yaml:"command"`
}

// Variant binds a version range to a set of step commands.
type Variant struct {
	When  string          `yaml:"when"`
	Steps map[string]Step `yaml:"steps"`
}

// Manifest describes a tool (or secret provider): which steps it provides, how to
// detect its version, and per-version-range command variants.
type Manifest struct {
	Tool         string    `yaml:"tool"`     // tool name (or Provider for secret stores)
	Provider     string    `yaml:"provider"` // set for secret providers instead of tool
	Detect       string    `yaml:"detect"`   // command whose stdout is the version
	Provides     []string  `yaml:"provides"`
	Variants     []Variant `yaml:"variants"`
	Incompatible string    `yaml:"incompatible,omitempty"`
	// Single-variant tools may declare steps at the top level instead of variants.
	Steps map[string]Step `yaml:"steps,omitempty"`
}

// Name returns the tool or provider name.
func (m Manifest) Name() string {
	if m.Tool != "" {
		return m.Tool
	}
	return m.Provider
}

// Provides reports whether the manifest provides the given abstract step.
func (m Manifest) ProvidesStep(step string) bool {
	for _, p := range m.Provides {
		if p == step {
			return true
		}
	}
	return false
}

// CommandFor returns the command template for `step` given the installed tool
// `version`. It checks `incompatible` first, then picks the FIRST variant whose
// `when` range matches; a manifest with top-level `steps` (no variants) ignores
// version. Returns ("", false, nil) when the step isn't provided by any matching
// variant.
func (m Manifest) CommandFor(step, version string) (string, bool, error) {
	if m.Incompatible != "" {
		bad, err := matchRange(version, m.Incompatible)
		if err != nil {
			return "", false, err
		}
		if bad {
			return "", false, fmt.Errorf("tool %s version %s is incompatible (%s)", m.Name(), version, m.Incompatible)
		}
	}
	if len(m.Steps) > 0 { // version-independent
		if s, ok := m.Steps[step]; ok {
			return s.Command, true, nil
		}
	}
	for _, v := range m.Variants {
		ok, err := matchRange(version, v.When)
		if err != nil {
			return "", false, err
		}
		if !ok {
			continue
		}
		if s, has := v.Steps[step]; has {
			return s.Command, true, nil
		}
	}
	return "", false, nil
}

// Registry holds the loaded manifests, keyed by tool/provider name.
type Registry struct {
	byName map[string]Manifest
}

// Load reads the embedded built-in manifests. (Repo-local / user overrides are a
// later milestone — see PLUGIN-MODEL.md "Manifest sourcing".)
func Load() (*Registry, error) {
	r := &Registry{byName: map[string]Manifest{}}
	entries, err := builtin.ReadDir("manifests")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := builtin.ReadFile(path.Join("manifests", e.Name()))
		if err != nil {
			return nil, err
		}
		var m Manifest
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("parse manifest %s: %w", e.Name(), err)
		}
		r.byName[m.Name()] = m
	}
	return r, nil
}

// Get returns the manifest for a tool/provider name.
func (r *Registry) Get(name string) (Manifest, bool) {
	m, ok := r.byName[name]
	return m, ok
}
