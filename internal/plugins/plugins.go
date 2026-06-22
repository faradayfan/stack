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

//go:embed managers/*.yaml
var managerFS embed.FS

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
	Tool           string       `yaml:"tool"`     // tool name (or Provider for secret stores)
	Provider       string       `yaml:"provider"` // set for secret providers instead of tool
	Detect         string       `yaml:"detect"`   // command whose stdout is the version
	VersionPattern string       `yaml:"version_pattern,omitempty"`
	Setup          *Setup       `yaml:"setup,omitempty"` // how `stack setup` installs/verifies it
	Config         []ConfigKey  `yaml:"config,omitempty"` // accepted per-tool config keys (for validation)
	Provides       []string     `yaml:"provides"`
	Variants       []Variant    `yaml:"variants"`
	Incompatible   string       `yaml:"incompatible,omitempty"`
	// Single-variant tools may declare steps at the top level instead of variants.
	Steps map[string]Step `yaml:"steps,omitempty"`
}

// ConfigKey describes one per-tool config key the manifest accepts.
type ConfigKey struct {
	Name     string `yaml:"name"`
	Required bool   `yaml:"required,omitempty"`
}

// ValidateConfig checks a binding's config against the manifest's declared keys:
// required keys must be present, and unknown keys are rejected (catches typos).
// A manifest with no `config:` declaration accepts anything (no validation).
func (m Manifest) ValidateConfig(cfg map[string]any) error {
	if len(m.Config) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, k := range m.Config {
		known[k.Name] = true
		if k.Required {
			if _, ok := cfg[k.Name]; !ok {
				return fmt.Errorf("tool %q requires config key %q", m.Name(), k.Name)
			}
		}
	}
	for k := range cfg {
		if !known[k] {
			return fmt.Errorf("tool %q: unknown config key %q", m.Name(), k)
		}
	}
	return nil
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

// Registry holds the loaded manifests, keyed by tool/provider name, plus the
// tools-manager manifests keyed by manager name.
type Registry struct {
	byName   map[string]Manifest
	managers map[string]Manager
}

// Load reads the embedded built-in tool + manager manifests. (Repo-local / user
// overrides are a later milestone — see PLUGIN-MODEL.md "Manifest sourcing".)
func Load() (*Registry, error) {
	r := &Registry{byName: map[string]Manifest{}, managers: map[string]Manager{}}
	if err := loadEach(builtin, "manifests", func(b []byte, name string) error {
		var m Manifest
		if err := yaml.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("parse manifest %s: %w", name, err)
		}
		r.byName[m.Name()] = m
		return nil
	}); err != nil {
		return nil, err
	}
	if err := loadEach(managerFS, "managers", func(b []byte, name string) error {
		var m Manager
		if err := yaml.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("parse manager %s: %w", name, err)
		}
		r.managers[m.Manager] = m
		return nil
	}); err != nil {
		return nil, err
	}
	return r, nil
}

func loadEach(fsys embed.FS, dir string, fn func(b []byte, name string) error) error {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := fsys.ReadFile(path.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if err := fn(b, e.Name()); err != nil {
			return err
		}
	}
	return nil
}

// Get returns the manifest for a tool/provider name.
func (r *Registry) Get(name string) (Manifest, bool) {
	m, ok := r.byName[name]
	return m, ok
}

// Manager returns the tools-manager manifest for a manager name.
func (r *Registry) Manager(name string) (Manager, bool) {
	m, ok := r.managers[name]
	return m, ok
}
