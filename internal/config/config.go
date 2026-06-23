// Package config loads the stack context files and resolves them into the
// pattern the engine runs.
//
// Schema v2 (see docs/SCHEMA-V2.md): .stack/app.yaml declares the deployment
// shapes the app supports as `patterns.<name>` — each a complete template (its
// type, images, per-step tool+config, scan policy, checks, hooks, identity). A
// .stack/<env>.yaml selects one pattern (`pattern: <name>`) and deep-merges its
// overrides into that template.
//
// The merge rule is uniform everywhere: env value ▸ pattern template ▸ default;
// maps merge by key, scalars replace, lists replace. Nothing else to learn.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Artifact is one buildable thing in a pattern's `artifacts:` map. Its fields are
// read by its build TOOL's manifest, so they're a superset across tool types:
//
//   - docker (k8s pattern): context, tag, args → an image ref
//   - go (native pattern):  package, output    → a binary
//
// Its Name is the map key (filled from the key, not parsed).
type Artifact struct {
	Name string `yaml:"-"`

	// image fields (docker / k8s)
	Context string            `yaml:"context,omitempty"`
	Tag     string            `yaml:"tag,omitempty"` // explicit tag wins over the env/default tag
	Args    map[string]string `yaml:"args,omitempty"`

	// binary fields (go / native)
	Package string `yaml:"package,omitempty"` // the Go package to build (e.g. ./cmd/stack)
	Output  string `yaml:"output,omitempty"`  // output path (e.g. bin/stack)
	Ldflags string `yaml:"ldflags,omitempty"`
}

// Scan is the scan step's policy: which first-party images to vuln-gate and the
// grype threshold. It lives in the same `scan:` block as the scan tool.
type Scan struct {
	Images []string `yaml:"images,omitempty"`
	FailOn string   `yaml:"fail_on,omitempty"` // grype severity; default "high"
}

// Check is one verification entry in the `stack check` flow — "run one tool, get
// pass/fail". Its name is the MAP KEY in a pattern's `checks`.
type Check struct {
	Name     string         `yaml:"-"`
	Tool     string         `yaml:"tool"`
	Blocking *bool          `yaml:"blocking,omitempty"` // nil/true → failure fails the run; false → report-only
	After    string         `yaml:"after,omitempty"`    // depend on a prior step (e.g. build-artifact)
	Serial   bool           `yaml:"serial,omitempty"`   // must not run alongside other checks
	Dir      string         `yaml:"dir,omitempty"`      // run from this subdir (e.g. frontend)
	Args     map[string]any `yaml:"args,omitempty"`     // template inputs to the tool's check command
}

// IsBlocking reports whether a failure of this check fails the run (default true).
func (c Check) IsBlocking() bool { return c.Blocking == nil || *c.Blocking }

// StepBlock is one abstract step's wiring inside a pattern: which tool performs
// it, plus that step's config/policy. The recognized fields (Tool) are typed; the
// rest of the block (chart, values, set, repos, node, delivery, images, fail_on,
// …) stays in Config so each tool reads its own keys with zero schema change. A
// step block decodes from a bare string ("build: docker") or an object.
type StepBlock struct {
	Tool   string         `yaml:"tool"`
	Config map[string]any `yaml:"-"` // every non-`tool` key in the block
}

// Pattern is one deployment shape (patterns.<name> in app.yaml). It is the
// complete template: engine type, identity, images, per-step blocks, checks,
// hooks. An env merges its overrides into a copy of this.
type Pattern struct {
	// identity — properties of the deployment shape (env may override).
	KubeContext   string `yaml:"kube_context,omitempty"`
	Namespace     string `yaml:"namespace,omitempty"`
	ImageDelivery string `yaml:"image_delivery,omitempty"` // load | push
	Registry      string `yaml:"registry,omitempty"`
	Platform      string `yaml:"platform,omitempty"`
	Tag           string `yaml:"tag,omitempty"` // env-wide tag (may be a template, e.g. {{ git_short_sha }})
	Remote        bool   `yaml:"remote,omitempty"`
	ReleaseName   string `yaml:"release_name,omitempty"`
	DefaultTag    string `yaml:"default_tag,omitempty"`

	// Pipeline is the ordered list of fine-grained stages the pattern runs
	// (check, build, scan, deliver, apply, wait). A forward verb runs the pipeline
	// up to and including its terminal stage (check→check, build→build, deploy→the
	// last stage), so list order IS gating order — put `check` first and
	// everything after is gated by it. A pattern IS its pipeline + step blocks;
	// there is no `type` (each stage runs its step block's tool).
	Pipeline []string `yaml:"pipeline,omitempty"`

	Artifacts map[string]Artifact  `yaml:"artifacts,omitempty"`
	Steps     map[string]StepBlock `yaml:"-"` // build/deliver/scan/apply/wait_ready/status/render

	Checks map[string]Check  `yaml:"checks,omitempty"`
	Hooks  map[string]string `yaml:"hooks,omitempty"`
}

// stepKeys are the pattern keys that decode into Steps (each a StepBlock). They
// are recognized by name so the rest of the pattern keys stay strongly typed.
var stepKeys = map[string]bool{
	"build": true, "deliver": true, "scan": true, "render": true,
	"apply": true, "wait_ready": true, "status": true, "logs": true,
	"teardown": true, "destroy": true,
}

// App is .stack/app.yaml: app-global identity + the patterns it supports.
type App struct {
	Name         string             `yaml:"name"`
	ToolsManager string             `yaml:"tools_manager,omitempty"`
	Patterns     map[string]Pattern `yaml:"patterns"`
}

// Resolved is one pattern resolved for a run (template merged with the selected
// env's overrides). The engine consumes this — it never sees the raw layers.
type Resolved struct {
	App          string // app name
	ToolsManager string // app-global (for the setup flow)
	EnvName      string // selected env (empty for the check flow)
	Name         string // pattern name
	Pattern      Pattern
}

// --- decoding: patterns and step blocks pull their "extra" keys into maps ------

// UnmarshalYAML for StepBlock accepts a bare string (tool, no config) or an
// object whose `tool` is typed and whose remaining keys become Config.
func (s *StepBlock) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode { // "build: docker"
		s.Tool = node.Value
		return nil
	}
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		return err
	}
	if t, ok := raw["tool"].(string); ok {
		s.Tool = t
	}
	delete(raw, "tool")
	if len(raw) > 0 {
		s.Config = raw
	}
	return nil
}

// UnmarshalYAML for Pattern decodes the typed fields, then sweeps the recognized
// step keys (build/deliver/scan/…) into Steps.
func (p *Pattern) UnmarshalYAML(node *yaml.Node) error {
	type rawPattern Pattern // avoid recursion
	var rp rawPattern
	if err := node.Decode(&rp); err != nil {
		return err
	}
	*p = Pattern(rp)

	// Second pass: collect the step blocks by their known keys.
	var all map[string]yaml.Node
	if err := node.Decode(&all); err != nil {
		return err
	}
	steps := map[string]StepBlock{}
	for k := range stepKeys {
		n, ok := all[k]
		if !ok {
			continue
		}
		var sb StepBlock
		if err := n.Decode(&sb); err != nil {
			return fmt.Errorf("step %q: %w", k, err)
		}
		steps[k] = sb
	}
	if len(steps) > 0 {
		p.Steps = steps
	}
	return nil
}

// --- loading -------------------------------------------------------------------

// StackDir is the per-repo context directory.
const StackDir = ".stack"

// LoadApp reads .stack/app.yaml (the patterns + app-global fields).
func LoadApp(repoRoot string) (App, error) {
	var a App
	if err := readYAML(filepath.Join(repoRoot, StackDir, "app.yaml"), &a); err != nil {
		return a, fmt.Errorf("load app config: %w", err)
	}
	if a.Name == "" {
		return a, fmt.Errorf("app.yaml: name is required")
	}
	if len(a.Patterns) == 0 {
		return a, fmt.Errorf("app.yaml: at least one pattern is required")
	}
	return a, nil
}

// Load reads app.yaml + .stack/<env>.yaml, selects the env's pattern, and deep-
// merges the env overrides into that pattern's template. The merged Pattern is
// returned in Resolved, ready for the engine.
//
// The merge happens on GENERIC trees (so the uniform map/scalar/list rule applies
// untyped), and only the merged result is decoded into a Pattern. The selected
// pattern's template subtree is taken from app.yaml-as-tree — not by re-encoding
// the typed Pattern, which would lose the step blocks (they decode out of the
// struct via the `-` tag).
func Load(repoRoot, envName string) (Resolved, error) {
	app, err := LoadApp(repoRoot)
	if err != nil {
		return Resolved{}, err
	}

	envPath := filepath.Join(repoRoot, StackDir, envName+".yaml")
	envTree, err := readTree(envPath)
	if err != nil {
		return Resolved{}, fmt.Errorf("load env %q: %w", envName, err)
	}
	patName, _ := envTree["pattern"].(string)
	if patName == "" {
		return Resolved{}, fmt.Errorf("%s.yaml: `pattern` is required (the app.yaml pattern to use)", envName)
	}
	if _, ok := app.Patterns[patName]; !ok {
		return Resolved{}, fmt.Errorf("%s.yaml: pattern %q is not defined in app.yaml", envName, patName)
	}

	// Pull the selected pattern's RAW subtree from app.yaml-as-tree.
	appTree, err := readTree(filepath.Join(repoRoot, StackDir, "app.yaml"))
	if err != nil {
		return Resolved{}, err
	}
	patsAny, _ := appTree["patterns"].(map[string]any)
	tmplTree, _ := asMap(patsAny[patName])
	if tmplTree == nil {
		tmplTree = map[string]any{}
	}

	// `pattern` is the selector, not a pattern field — drop before merging.
	delete(envTree, "pattern")
	merged := mergeTree(tmplTree, envTree)

	var pat Pattern
	if err := decodeTree(merged, &pat); err != nil {
		return Resolved{}, fmt.Errorf("%s.yaml: resolve pattern %q: %w", envName, patName, err)
	}

	r := Resolved{App: app.Name, ToolsManager: app.ToolsManager, EnvName: envName, Name: patName, Pattern: pat}
	if err := r.validate(); err != nil {
		return r, err
	}
	return r, nil
}

// LoadPattern resolves a pattern straight off app.yaml, with no env overrides —
// for apps (or verbs) that need no per-environment file. `name` selects the
// pattern; empty auto-selects when the app has exactly one. An env file is only
// needed when you actually have environment-specific overrides; otherwise a
// pattern is runnable directly (`stack <verb> --pattern <name>`).
func LoadPattern(repoRoot, name string) (Resolved, error) {
	app, err := LoadApp(repoRoot)
	if err != nil {
		return Resolved{}, err
	}
	patName, pat, err := app.SelectPattern(name)
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{App: app.Name, ToolsManager: app.ToolsManager, Name: patName, Pattern: pat}
	if err := r.validate(); err != nil {
		return r, err
	}
	return r, nil
}

// SelectPattern returns the named pattern (or the sole one if name is empty and
// there's exactly one). Used by the check flow, which is env-independent.
func (a App) SelectPattern(name string) (string, Pattern, error) {
	if name == "" {
		if len(a.Patterns) == 1 {
			for n, p := range a.Patterns {
				return n, p, nil
			}
		}
		names := a.PatternNames()
		return "", Pattern{}, fmt.Errorf("app has %d patterns (%v); pass --pattern to choose one", len(names), names)
	}
	p, ok := a.Patterns[name]
	if !ok {
		return "", Pattern{}, fmt.Errorf("pattern %q is not defined in app.yaml (have %v)", name, a.PatternNames())
	}
	return name, p, nil
}

// PatternNames returns the pattern names sorted.
func (a App) PatternNames() []string {
	out := make([]string, 0, len(a.Patterns))
	for n := range a.Patterns {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (r Resolved) validate() error {
	if len(r.Pattern.Pipeline) == 0 {
		return fmt.Errorf("pattern %q: a `pipeline` is required (e.g. [build] or [check, build, deliver, apply])", r.Name)
	}
	return nil
}

// --- accessors the engine uses -------------------------------------------------

// Step returns the resolved step block (tool + config) for an abstract step.
func (p Pattern) Step(step string) (StepBlock, bool) {
	s, ok := p.Steps[step]
	return s, ok
}

// Scan returns the resolved scan policy (from the scan step block's config).
func (p Pattern) Scan() Scan {
	var s Scan
	sb, ok := p.Steps["scan"]
	if !ok {
		return s
	}
	if imgs, ok := sb.Config["images"].([]any); ok {
		for _, v := range imgs {
			if str, ok := v.(string); ok {
				s.Images = append(s.Images, str)
			}
		}
	}
	if f, ok := sb.Config["fail_on"].(string); ok {
		s.FailOn = f
	}
	return s
}

// Identity returns the cross-tool values merged into every step's template inputs.
func (p Pattern) Identity() map[string]any {
	return map[string]any{
		"kube_context": p.KubeContext,
		"namespace":    p.Namespace,
		"delivery":     p.ImageDelivery,
	}
}

// ReleaseName is the helm release name: explicit release_name, else the app name.
func (r Resolved) ReleaseName() string {
	if r.Pattern.ReleaseName != "" {
		return r.Pattern.ReleaseName
	}
	return r.App
}

// DefaultTag resolves the pattern's default_tag ▸ "dev".
func (p Pattern) ResolvedDefaultTag() string {
	if p.DefaultTag != "" {
		return p.DefaultTag
	}
	return "dev"
}

// SortedArtifacts returns the pattern's artifacts in deterministic key order, named.
func (p Pattern) SortedArtifacts() []Artifact {
	keys := make([]string, 0, len(p.Artifacts))
	for k := range p.Artifacts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Artifact, 0, len(keys))
	for _, k := range keys {
		a := p.Artifacts[k]
		a.Name = k
		out = append(out, a)
	}
	return out
}

// ArtifactByName returns the pattern artifact for a name (with Name set).
func (p Pattern) ArtifactByName(name string) (Artifact, bool) {
	a, ok := p.Artifacts[name]
	if !ok {
		return Artifact{}, false
	}
	a.Name = name
	return a, true
}

// ImageRef returns [registry/]name:tag for an image artifact. tag precedence: the
// artifact's own tag ▸ envTag (the resolved pattern Tag) ▸ default_tag. registry
// prefixes when set.
func (p Pattern) ImageRef(a Artifact, envTag string) string {
	tag := firstNonEmpty(a.Tag, envTag, p.ResolvedDefaultTag())
	ref := a.Name + ":" + tag
	if p.Registry != "" {
		ref = p.Registry + "/" + ref
	}
	return ref
}

// SortedChecks returns the pattern's checks in deterministic key order, named.
func (p Pattern) SortedChecks() []Check {
	keys := make([]string, 0, len(p.Checks))
	for k := range p.Checks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Check, 0, len(keys))
	for _, k := range keys {
		c := p.Checks[k]
		c.Name = k
		out = append(out, c)
	}
	return out
}

// --- helpers -------------------------------------------------------------------

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
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
