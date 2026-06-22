// Package config loads and merges the two stack context-file layers:
// .stack/app.yaml (app-wide, environment-independent) and .stack/<env>.yaml
// (one per environment). The merged result drives the engine.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Image is one buildable artifact. Its name is the MAP KEY in `images`, so there
// is no `name` field here.
type Image struct {
	Name    string            `yaml:"-"` // filled from the map key (not parsed)
	Context string            `yaml:"context"`
	Tag     string            `yaml:"tag,omitempty"`  // explicit tag wins over the env/default tag
	Args    map[string]string `yaml:"args,omitempty"` // --build-arg
}

// Scan declares which first-party images to vuln-gate and at what threshold.
type Scan struct {
	Images []string `yaml:"images,omitempty"`
	FailOn string   `yaml:"fail_on,omitempty"` // grype severity; default "high"
}

// Settings are the OVERRIDABLE deploy/runtime settings shared by App (the base
// layer) and Env (the override layer). Resolution is env-value ▸ app-value ▸
// built-in default (see Resolved). Identity fields (name, images, checks) are NOT
// here — they are not env-overridable by design.
type Settings struct {
	ToolsManager string `yaml:"tools_manager,omitempty"`
	DefaultTag   string `yaml:"default_tag,omitempty"` // tag for images that don't pin their own
	Registry     string `yaml:"registry,omitempty"`    // push: prefix image refs
	Platform     string `yaml:"platform,omitempty"`    // push: buildx --platform
	Scan         *Scan  `yaml:"scan,omitempty"`        // pointer → distinguish "unset" from zero for override
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
// pass/fail". Its name is the MAP KEY in `checks`, so there is no `name` field.
// Atomic by design: if a check needs real logic, it's a hook instead.
type Check struct {
	Name     string         `yaml:"-"`                  // filled from the map key (not parsed)
	Tool     string         `yaml:"tool"`               // a plugin providing the `check` step
	Blocking *bool          `yaml:"blocking,omitempty"` // nil/true → failure fails the run; false → report-only
	After    string         `yaml:"after,omitempty"`    // depend on a prior step (e.g. build-artifact)
	Serial   bool           `yaml:"serial,omitempty"`   // must not run alongside other checks
	Dir      string         `yaml:"dir,omitempty"`      // run from this subdir (e.g. frontend)
	Args     map[string]any `yaml:"args,omitempty"`     // passed as template inputs to the tool's check command
}

// IsBlocking reports whether a failure of this check fails the run (default true).
func (c Check) IsBlocking() bool { return c.Blocking == nil || *c.Blocking }

// SortedChecks returns the app's checks in deterministic key order, each carrying
// its map-key Name. The check flow is env-independent, so this is on App.
func (a App) SortedChecks() []Check {
	keys := make([]string, 0, len(a.Checks))
	for k := range a.Checks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Check, 0, len(keys))
	for _, k := range keys {
		c := a.Checks[k]
		c.Name = k
		out = append(out, c)
	}
	return out
}

// App is .stack/app.yaml — the BASE layer. Identity (name) + collections
// (images, checks) live here; overridable Settings are embedded and may be
// overridden per-env.
type App struct {
	Name     string            `yaml:"name"`
	Settings `yaml:",inline"`  // overridable base settings
	Images   map[string]Image  `yaml:"images,omitempty"` // key = image name
	Checks   map[string]Check  `yaml:"checks,omitempty"` // key = check name
	Hooks    map[string]string `yaml:"hooks,omitempty"`
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
	Tag           string `yaml:"tag,omitempty"`            // env-wide image tag (may be a template, e.g. {{ git_short_sha }})
	Remote        bool   `yaml:"remote,omitempty"`         // → confirm before deploy/down
	ReleaseName   string `yaml:"release_name,omitempty"`

	// overridable settings — env values override the app's (env ▸ app ▸ default)
	Settings `yaml:",inline"`

	// per-key overrides of the app's collections (merged by key, env wins)
	Images map[string]Image `yaml:"images,omitempty"`
	Checks map[string]Check `yaml:"checks,omitempty"`

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

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// ToolsManager resolves env ▸ app (no built-in default; empty → setup errors).
func (m Merged) ToolsManager() string {
	return firstNonEmpty(m.Env.Settings.ToolsManager, m.App.Settings.ToolsManager)
}

// DefaultTag resolves env ▸ app ▸ "dev".
func (m Merged) DefaultTag() string {
	return firstNonEmpty(m.Env.Settings.DefaultTag, m.App.Settings.DefaultTag, "dev")
}

// Registry resolves env ▸ app (empty → no registry prefix, i.e. local load).
func (m Merged) Registry() string {
	return firstNonEmpty(m.Env.Settings.Registry, m.App.Settings.Registry)
}

// Platform resolves env ▸ app (empty → tool default).
func (m Merged) Platform() string {
	return firstNonEmpty(m.Env.Settings.Platform, m.App.Settings.Platform)
}

// Scan resolves env ▸ app (whole-value override; pointer distinguishes unset).
func (m Merged) Scan() Scan {
	if m.Env.Settings.Scan != nil {
		return *m.Env.Settings.Scan
	}
	if m.App.Settings.Scan != nil {
		return *m.App.Settings.Scan
	}
	return Scan{}
}

// Images returns the app images overlaid with the env's per-key overrides (env
// wins per key; env may also add images), each carrying its map-key Name.
func (m Merged) Images() map[string]Image {
	out := map[string]Image{}
	for k, v := range m.App.Images {
		out[k] = v
	}
	for k, v := range m.Env.Images {
		out[k] = v // whole-value override per key (no field-level merge)
	}
	return out
}

// SortedImages returns the merged images in deterministic key order, each named.
func (m Merged) SortedImages() []Image {
	merged := m.Images()
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Image, 0, len(keys))
	for _, k := range keys {
		img := merged[k]
		img.Name = k
		out = append(out, img)
	}
	return out
}

// ImageByName returns the merged image for a name (with Name set).
func (m Merged) ImageByName(name string) (Image, bool) {
	img, ok := m.Images()[name]
	if !ok {
		return Image{}, false
	}
	img.Name = name
	return img, true
}

// ReleaseName is the helm release name: explicit release_name, else the app name.
func (m Merged) ReleaseName() string {
	if m.Env.ReleaseName != "" {
		return m.Env.ReleaseName
	}
	return m.App.Name
}

// ImageRef returns the image reference: [registry/]name:tag.
//
//   - registry: resolved registry prefix (push delivery), empty for local load.
//   - tag precedence: the image's OWN tag (explicit, e.g. postgres' 16-pgvector)
//     ▸ envTag (a resolved env-wide tag like a git sha, applied to images that
//     don't pin their own) ▸ resolved DefaultTag. envTag is passed in already
//     resolved (templates like {{ git_short_sha }} are expanded by the engine).
func (m Merged) ImageRef(img Image, envTag string) string {
	tag := firstNonEmpty(img.Tag, envTag, m.DefaultTag())
	ref := img.Name + ":" + tag
	if reg := m.Registry(); reg != "" {
		ref = reg + "/" + ref
	}
	return ref
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
