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
  - { name: baseline, context: . }
  - { name: ui, context: ./frontend, tag: "16-x" }
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
	// tag defaulting
	if got := m.ImageRef(m.App.Images[0]); got != "baseline:dev" {
		t.Errorf("default tag: got %s", got)
	}
	if got := m.ImageRef(m.App.Images[1]); got != "ui:16-x" {
		t.Errorf("explicit tag: got %s", got)
	}
	if m.ReleaseName() != "baseline" {
		t.Errorf("release name: got %s", m.ReleaseName())
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
