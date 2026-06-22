package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeCtx creates a .stack dir with app.yaml + the named env file.
func writeCtx(t *testing.T, app, env, envName string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, StackDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, envName+".yaml"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadAndMerge(t *testing.T) {
	root := writeCtx(t,
		`name: baseline
default_tag: dev
images:
  baseline: { context: . }
  ui: { context: ./frontend, tag: "16-x" }
scan: { images: [baseline], fail_on: high }`,
		`pattern: k8s
namespace: baseline
tools: { build-artifact: docker, apply: helm }`,
		"local-k8s")

	m, err := Load(root, "local-k8s")
	if err != nil {
		t.Fatal(err)
	}
	if m.App.Name != "baseline" || m.Env.Pattern != "k8s" {
		t.Fatalf("merge wrong: %+v", m)
	}
	base, _ := m.ImageByName("baseline")
	ui, _ := m.ImageByName("ui")
	// tag defaulting (no env tag → resolved default "dev")
	if got := m.ImageRef(base, ""); got != "baseline:dev" {
		t.Errorf("default tag: got %s", got)
	}
	if got := m.ImageRef(ui, ""); got != "ui:16-x" {
		t.Errorf("explicit tag: got %s", got)
	}
	if m.ReleaseName() != "baseline" {
		t.Errorf("release name: got %s", m.ReleaseName())
	}
}

// TestSettingsResolution: env value ▸ app value ▸ built-in default, per setting.
func TestSettingsResolution(t *testing.T) {
	root := writeCtx(t,
		`name: baseline
tools_manager: asdf
default_tag: dev
images:
  baseline: { context: . }`,
		`pattern: k8s
registry: registry.example:5000
tag: abc123
tools: { apply: helm }`,
		"pi")
	m, err := Load(root, "pi")
	if err != nil {
		t.Fatal(err)
	}
	// app-only setting (env doesn't override) → app value
	if got := m.ToolsManager(); got != "asdf" {
		t.Errorf("tools_manager: got %q want asdf", got)
	}
	// env-only setting → env value (registry: app empty)
	if got := m.Registry(); got != "registry.example:5000" {
		t.Errorf("registry: got %q", got)
	}
	// default_tag: only app sets it → app value
	if got := m.DefaultTag(); got != "dev" {
		t.Errorf("default_tag: got %q", got)
	}
	// env-wide tag applies to images that don't pin their own; registry prefixes.
	base, _ := m.ImageByName("baseline")
	if got := m.ImageRef(base, m.Env.Tag); got != "registry.example:5000/baseline:abc123" {
		t.Errorf("env-tag+registry ref: got %s", got)
	}
}

// TestSettingsOverride: an env Settings value overrides the app's.
func TestSettingsOverride(t *testing.T) {
	root := writeCtx(t,
		`name: x
default_tag: dev
images: { a: { context: . } }`,
		`pattern: k8s
default_tag: prod
tools: { apply: helm }`,
		"e")
	m, err := Load(root, "e")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.DefaultTag(); got != "prod" {
		t.Errorf("env override of default_tag: got %q want prod", got)
	}
}

// TestImageMapMerge: env images merge by key — override an existing image and add
// a new one; SortedImages is deterministic (alphabetical) and carries Name.
func TestImageMapMerge(t *testing.T) {
	root := writeCtx(t,
		`name: x
images:
  api: { context: ., tag: app-tag }
  ui:  { context: ./frontend }`,
		`pattern: k8s
images:
  api: { context: ., tag: env-tag }   # override
  worker: { context: ./worker }       # add
tools: { apply: helm }`,
		"e")
	m, err := Load(root, "e")
	if err != nil {
		t.Fatal(err)
	}
	imgs := m.SortedImages()
	// deterministic, alphabetical: api, ui, worker
	wantOrder := []string{"api", "ui", "worker"}
	if len(imgs) != 3 {
		t.Fatalf("want 3 images, got %d: %+v", len(imgs), imgs)
	}
	for i, w := range wantOrder {
		if imgs[i].Name != w {
			t.Errorf("image[%d] = %q want %q (sort order)", i, imgs[i].Name, w)
		}
	}
	// env override won
	api, _ := m.ImageByName("api")
	if api.Tag != "env-tag" {
		t.Errorf("env override of api.tag: got %q want env-tag", api.Tag)
	}
}

func TestLoad_Validation(t *testing.T) {
	// missing pattern → error
	root := writeCtx(t, `name: x`, `tools: { apply: helm }`, "e")
	if _, err := Load(root, "e"); err == nil {
		t.Error("expected error for missing pattern")
	}
}

func TestStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STACK_HOME", home)
	repo := t.TempDir()

	if s, _ := LoadState(repo); s.CurrentEnv != "" {
		t.Error("fresh state should be empty")
	}
	if err := SaveState(repo, State{CurrentEnv: "pi"}); err != nil {
		t.Fatal(err)
	}
	s, err := LoadState(repo)
	if err != nil || s.CurrentEnv != "pi" {
		t.Fatalf("round-trip: %+v, %v", s, err)
	}
	// a different repo path has independent state
	other := t.TempDir()
	if s, _ := LoadState(other); s.CurrentEnv != "" {
		t.Error("state must be per-repo")
	}
}

// TestToolBinding_StringOrObject: a tool binding parses from both a bare string
// (no config) and a {tool, config} object.
func TestToolBinding_StringOrObject(t *testing.T) {
	root := writeCtx(t,
		`name: x`,
		`pattern: k8s
kube_context: docker-desktop
namespace: ns
image_delivery: load
tools:
  scan-artifact: grype
  apply:
    tool: helm
    config:
      chart: deploy/charts/x
      values: [a.yaml, b.yaml]`,
		"e")
	m, err := Load(root, "e")
	if err != nil {
		t.Fatal(err)
	}
	// string form
	scan := m.Env.Tools["scan-artifact"]
	if scan.Tool != "grype" || scan.Config != nil {
		t.Errorf("string binding: got tool=%q config=%v", scan.Tool, scan.Config)
	}
	// object form
	apply := m.Env.Tools["apply"]
	if apply.Tool != "helm" {
		t.Errorf("object binding tool = %q, want helm", apply.Tool)
	}
	if apply.Config["chart"] != "deploy/charts/x" {
		t.Errorf("object binding config.chart = %v", apply.Config["chart"])
	}
	// identity is separate from tool config
	id := m.Env.Identity()
	if id["kube_context"] != "docker-desktop" || id["namespace"] != "ns" {
		t.Errorf("identity wrong: %v", id)
	}
}

func TestListEnvs(t *testing.T) {
	root := writeCtx(t, `name: x`, `pattern: k8s
tools: { apply: helm }`, "local-k8s")
	// add a second env
	os.WriteFile(filepath.Join(root, StackDir, "pi.yaml"), []byte("pattern: k8s\ntools: {apply: helm}"), 0o644)
	envs, err := ListEnvs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 {
		t.Errorf("want 2 envs (local-k8s, pi), got %v", envs)
	}
	for _, e := range envs {
		if e == "app" {
			t.Error("app.yaml must not be listed as an env")
		}
	}
}
