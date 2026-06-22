// Package config loads and merges the two stack context-file layers:
// .stack/app.yaml (app-wide, environment-independent) and .stack/<env>.yaml
// (one per environment). The merged result drives the engine.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Image is one buildable artifact.
type Image struct {
	Name    string            `yaml:"name"`
	Context string            `yaml:"context"`
	Tag     string            `yaml:"tag,omitempty"`  // defaults to App.DefaultTag
	Args    map[string]string `yaml:"args,omitempty"` // --build-arg
}

// Scan declares which first-party images to vuln-gate and at what threshold.
type Scan struct {
	Images []string `yaml:"images"`
	FailOn string   `yaml:"fail_on,omitempty"` // grype severity; default "high"
}

// HelmRepo is a chart repo to register before `helm dependency build`.
type HelmRepo struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Deps are prerequisites resolved before render/apply.
type Deps struct {
	HelmRepos []HelmRepo `yaml:"helm_repos,omitempty"`
}

// Check is one verification entry in the `stack check` flow — "run one tool, get
// pass/fail". Atomic by design: if a check needs real logic, it's a hook instead.
type Check struct {
	Name     string         `yaml:"name"`
	Tool     string         `yaml:"tool"`               // a plugin providing the `check` step
	Blocking *bool          `yaml:"blocking,omitempty"` // nil/true → failure fails the run; false → report-only
	After    string         `yaml:"after,omitempty"`    // depend on a prior step (e.g. build-artifact)
	Serial   bool           `yaml:"serial,omitempty"`   // must not run alongside other checks
	Dir      string         `yaml:"dir,omitempty"`      // run from this subdir (e.g. frontend)
	Args     map[string]any `yaml:"args,omitempty"`     // passed as template inputs to the tool's check command
}

// IsBlocking reports whether a failure of this check fails the run (default true).
func (c Check) IsBlocking() bool { return c.Blocking == nil || *c.Blocking }

// App is .stack/app.yaml — app-wide, environment-independent.
type App struct {
	Name         string  `yaml:"name"`
	ToolsManager string  `yaml:"tools_manager,omitempty"` // e.g. "asdf"; empty → setup errors
	DefaultTag   string  `yaml:"default_tag,omitempty"`   // tag when an Image omits one; default "dev"
	Images       []Image `yaml:"images"`
	Scan         Scan    `yaml:"scan"`
	Checks       []Check `yaml:"checks,omitempty"` // the `stack check` flow
	// Hooks: name → command (M1 keeps the simple string form; the typed
	// env-mapping form lands with the native/seed milestone).
	Hooks map[string]string `yaml:"hooks,omitempty"`
}

// ToolBinding binds an abstract step to a tool plus that tool's per-step config.
// It accepts two YAML forms (string-or-object):
//
//	scan-artifact: grype                       # shorthand, no config
//	apply: { tool: helm, config: { chart: … } } # full form
type ToolBinding struct {
	Tool   string         `yaml:"tool"`
	Config map[string]any `yaml:"config,omitempty"`
}

// UnmarshalYAML implements the string-or-object decoding.
func (b *ToolBinding) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode { // bare string → tool name, no config
		b.Tool = node.Value
		return nil
	}
	// object form
	type raw ToolBinding
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*b = ToolBinding(r)
	if b.Tool == "" {
		return fmt.Errorf("tool binding object must set `tool`")
	}
	return nil
}

// Env is .stack/<env>.yaml — one per environment.
//
// Three tiers: (1) environment IDENTITY at the root (cross-tool, merged into
// every step's inputs); (2) tool BINDINGS in `tools` (step → tool); (3) per-tool
// CONFIG inside each binding. The engine merges identity + a step's tool config
// into that step's template inputs.
type Env struct {
	Pattern string `yaml:"pattern"` // k8s | native | compose

	// tier 1 — environment identity (cross-tool)
	KubeContext   string `yaml:"kube_context,omitempty"`
	Namespace     string `yaml:"namespace,omitempty"`
	ImageDelivery string `yaml:"image_delivery,omitempty"` // load | push
	Remote        bool   `yaml:"remote,omitempty"`         // → confirm before deploy/down
	ReleaseName   string `yaml:"release_name,omitempty"`

	// tier 2+3 — step → tool binding (+ that tool's config)
	Tools map[string]ToolBinding `yaml:"tools"`
}

// Identity returns the cross-tool environment values merged into every step's
// inputs (alongside the step's own tool config).
func (e Env) Identity() map[string]any {
	return map[string]any{
		"kube_context": e.KubeContext,
		"namespace":    e.Namespace,
		"delivery":     e.ImageDelivery,
	}
}

// Merged is the resolved app + env, ready for the engine.
type Merged struct {
	EnvName string
	App     App
	Env     Env
}

// ReleaseName is the helm release name: explicit release_name, else the app name.
func (m Merged) ReleaseName() string {
	if m.Env.ReleaseName != "" {
		return m.Env.ReleaseName
	}
	return m.App.Name
}

// ImageRef returns name:tag for an image (tag defaults to App.DefaultTag, then "dev").
func (m Merged) ImageRef(img Image) string {
	tag := img.Tag
	if tag == "" {
		tag = m.App.DefaultTag
	}
	if tag == "" {
		tag = "dev"
	}
	return img.Name + ":" + tag
}

// StackDir is the per-repo context directory.
const StackDir = ".stack"

// Load reads .stack/app.yaml and .stack/<env>.yaml from repoRoot and merges them.
func Load(repoRoot, envName string) (Merged, error) {
	var m Merged
	m.EnvName = envName

	appPath := filepath.Join(repoRoot, StackDir, "app.yaml")
	if err := readYAML(appPath, &m.App); err != nil {
		return m, fmt.Errorf("load app config: %w", err)
	}
	envPath := filepath.Join(repoRoot, StackDir, envName+".yaml")
	if err := readYAML(envPath, &m.Env); err != nil {
		return m, fmt.Errorf("load env %q: %w", envName, err)
	}
	if err := m.validate(); err != nil {
		return m, err
	}
	return m, nil
}

// LoadApp reads only .stack/app.yaml. The check flow is environment-independent,
// so `stack check` does not require a selected env.
func LoadApp(repoRoot string) (App, error) {
	var a App
	if err := readYAML(filepath.Join(repoRoot, StackDir, "app.yaml"), &a); err != nil {
		return a, fmt.Errorf("load app config: %w", err)
	}
	if a.Name == "" {
		return a, fmt.Errorf("app.yaml: name is required")
	}
	return a, nil
}

func (m Merged) validate() error {
	if m.App.Name == "" {
		return fmt.Errorf("app.yaml: name is required")
	}
	if m.Env.Pattern == "" {
		return fmt.Errorf("%s.yaml: pattern is required", m.EnvName)
	}
	if len(m.Env.Tools) == 0 {
		return fmt.Errorf("%s.yaml: tools mapping is required", m.EnvName)
	}
	return nil
}

func readYAML(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, dst)
}

// ListEnvs returns the available environment names (the <env>.yaml files in
// .stack/, excluding app.yaml).
func ListEnvs(repoRoot string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(repoRoot, StackDir))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || filepath.Ext(n) != ".yaml" || n == "app.yaml" {
			continue
		}
		out = append(out, n[:len(n)-len(".yaml")])
	}
	return out, nil
}
