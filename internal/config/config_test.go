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

const baseApp = `name: baseline
tools_manager: asdf
patterns:
  k8s:
    type: k8s
    default_tag: dev
    namespace: baseline
    artifacts:
      baseline: { context: . }
      ui:       { context: ./frontend, tag: "16-x" }
    build:   { tool: docker }
    deliver: { tool: docker, delivery: load }
    scan:    { tool: grype, images: [baseline], fail_on: high }
    apply:
      tool: helm
      chart: deploy/charts/baseline
      values: [deploy/local/values.yaml]
    checks:
      format: { tool: gofmt }
`

// TestLoadAndResolve: env selects the pattern; identity + steps resolve.
func TestLoadAndResolve(t *testing.T) {
	root := writeCtx(t, baseApp,
		`pattern: k8s
kube_context: docker-desktop`,
		"local-k8s")

	r, err := Load(root, "local-k8s")
	if err != nil {
		t.Fatal(err)
	}
	if r.App != "baseline" || r.Name != "k8s" || r.Pattern.Type != "k8s" {
		t.Fatalf("resolve wrong: %+v", r)
	}
	if r.Pattern.KubeContext != "docker-desktop" {
		t.Errorf("env kube_context not merged: %q", r.Pattern.KubeContext)
	}
	if r.Pattern.Namespace != "baseline" {
		t.Errorf("template namespace not inherited: %q", r.Pattern.Namespace)
	}
	// tag defaulting / explicit tag
	base, _ := r.Pattern.ArtifactByName("baseline")
	ui, _ := r.Pattern.ArtifactByName("ui")
	if got := r.Pattern.ImageRef(base, r.Pattern.Tag); got != "baseline:dev" {
		t.Errorf("default tag: got %s", got)
	}
	if got := r.Pattern.ImageRef(ui, r.Pattern.Tag); got != "ui:16-x" {
		t.Errorf("explicit tag: got %s", got)
	}
	if r.ReleaseName() != "baseline" {
		t.Errorf("release name: got %s", r.ReleaseName())
	}
	// scan policy resolves from the scan step block
	scan := r.Pattern.Scan()
	if len(scan.Images) != 1 || scan.Images[0] != "baseline" || scan.FailOn != "high" {
		t.Errorf("scan policy: %+v", scan)
	}
	// step tool binding
	build, ok := r.Pattern.Step("build")
	if !ok || build.Tool != "docker" {
		t.Errorf("build step: %+v ok=%v", build, ok)
	}
}

// TestResolve_StepBlockMerge: env overrides a single step-block leaf (delivery)
// while the template's tool is kept (map merge by key).
func TestResolve_StepBlockMerge(t *testing.T) {
	root := writeCtx(t, baseApp,
		`pattern: k8s
deliver: { delivery: push }`,
		"pi")
	r, err := Load(root, "pi")
	if err != nil {
		t.Fatal(err)
	}
	deliver, _ := r.Pattern.Step("deliver")
	if deliver.Tool != "docker" {
		t.Errorf("deliver.tool should survive merge: %q", deliver.Tool)
	}
	if deliver.Config["delivery"] != "push" {
		t.Errorf("deliver.delivery override: %v", deliver.Config["delivery"])
	}
}

// TestResolve_IdentityOverride: env overrides identity fields (registry/platform/
// tag) declared on the pattern, prefixing image refs.
func TestResolve_IdentityOverride(t *testing.T) {
	root := writeCtx(t, baseApp,
		`pattern: k8s
registry: reg.example:5000
platform: linux/arm64
tag: abc123`,
		"pi")
	r, err := Load(root, "pi")
	if err != nil {
		t.Fatal(err)
	}
	if r.Pattern.Registry != "reg.example:5000" || r.Pattern.Platform != "linux/arm64" {
		t.Errorf("identity not merged: %+v", r.Pattern)
	}
	base, _ := r.Pattern.ArtifactByName("baseline")
	if got := r.Pattern.ImageRef(base, r.Pattern.Tag); got != "reg.example:5000/baseline:abc123" {
		t.Errorf("env-tag+registry ref: got %s", got)
	}
}

// TestResolve_ImageMapMerge: env images merge by key — override one, add one.
func TestResolve_ImageMapMerge(t *testing.T) {
	root := writeCtx(t, baseApp,
		`pattern: k8s
artifacts:
  ui: { context: ./frontend, tag: env-tag }   # override
  worker: { context: ./worker }               # add
`,
		"e")
	r, err := Load(root, "e")
	if err != nil {
		t.Fatal(err)
	}
	imgs := r.Pattern.SortedArtifacts()
	wantOrder := []string{"baseline", "ui", "worker"}
	if len(imgs) != 3 {
		t.Fatalf("want 3 images, got %d: %+v", len(imgs), imgs)
	}
	for i, w := range wantOrder {
		if imgs[i].Name != w {
			t.Errorf("image[%d] = %q want %q", i, imgs[i].Name, w)
		}
	}
	ui, _ := r.Pattern.ArtifactByName("ui")
	if ui.Tag != "env-tag" {
		t.Errorf("env override of ui.tag: got %q", ui.Tag)
	}
}

// TestLoad_UnknownPattern errors clearly.
func TestLoad_UnknownPattern(t *testing.T) {
	root := writeCtx(t, baseApp, `pattern: nope`, "e")
	if _, err := Load(root, "e"); err == nil {
		t.Error("expected error for env selecting an undefined pattern")
	}
}

// TestLoad_MissingPatternField errors (env must select a pattern).
func TestLoad_MissingPatternField(t *testing.T) {
	root := writeCtx(t, baseApp, `kube_context: x`, "e")
	if _, err := Load(root, "e"); err == nil {
		t.Error("expected error for env with no `pattern`")
	}
}

// TestSelectPattern: auto-select when one, error when many w/o a name.
func TestSelectPattern(t *testing.T) {
	root := writeCtx(t, baseApp, `pattern: k8s`, "e")
	app, err := LoadApp(root)
	if err != nil {
		t.Fatal(err)
	}
	// one pattern → auto-select
	name, _, err := app.SelectPattern("")
	if err != nil || name != "k8s" {
		t.Fatalf("auto-select single pattern: name=%q err=%v", name, err)
	}
	// add a second pattern → must require a name
	app.Patterns["native"] = Pattern{Type: "native"}
	if _, _, err := app.SelectPattern(""); err == nil {
		t.Error("expected error selecting among multiple patterns without --pattern")
	}
	if _, _, err := app.SelectPattern("native"); err != nil {
		t.Errorf("explicit select should work: %v", err)
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
	other := t.TempDir()
	if s, _ := LoadState(other); s.CurrentEnv != "" {
		t.Error("state must be per-repo")
	}
}

func TestListEnvs(t *testing.T) {
	root := writeCtx(t, baseApp, `pattern: k8s`, "local-k8s")
	os.WriteFile(filepath.Join(root, StackDir, "pi.yaml"), []byte("pattern: k8s"), 0o644)
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
