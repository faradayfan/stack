package engine_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/faradayfan/stack/internal/config"
	"github.com/faradayfan/stack/internal/engine"
	"github.com/faradayfan/stack/internal/plugins"
)

// baselineLikeCfg mirrors baseline's .stack context (the M1 fixture) so the
// dry-run output can be asserted against the known make-local-up command stream.
func baselineLikeCfg() config.Merged {
	return config.Merged{
		EnvName: "local-k8s",
		App: config.App{
			Name:       "baseline",
			DefaultTag: "dev",
			Images: []config.Image{
				{Name: "baseline", Context: "."},
				{Name: "baseline-ui", Context: "./frontend"},
				{Name: "baseline-postgresql", Context: "./deploy/postgres", Tag: "16-pgvector"},
				{Name: "baseline-mem0-api", Context: "./deploy/mem0-api", Tag: "ollama", Args: map[string]string{"PATCH_OLLAMA": "1"}},
			},
			Scan: config.Scan{Images: []string{"baseline", "baseline-ui"}, FailOn: "high"},
		},
		Env: config.Env{
			Pattern:       "k8s",
			KubeContext:   "docker-desktop",
			Namespace:     "baseline",
			Node:          "desktop-control-plane",
			ImageDelivery: "load",
			Tools: map[string]string{
				"build-artifact": "docker", "deliver-artifact": "docker",
				"scan-artifact": "grype", "apply": "helm", "wait-ready": "helm",
				"teardown": "helm", "status": "kubectl",
			},
			Chart:   "deploy/charts/baseline",
			Values:  []string{"deploy/local/values.yaml"},
			HelmSet: map[string]string{"rollmeTimestamp": "{{ now_unix }}"},
			Deps:    config.Deps{HelmRepos: []config.HelmRepo{{Name: "bitnami", URL: "https://charts.bitnami.com/bitnami"}}},
		},
	}
}

func dryRun(t *testing.T, fn func(e *engine.Engine) error) string {
	t.Helper()
	reg, err := plugins.Load()
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(baselineLikeCfg(), reg, true)
	var buf bytes.Buffer
	e.Out = &buf
	if err := fn(e); err != nil {
		t.Fatalf("dry-run errored: %v", err)
	}
	return buf.String()
}

// TestDeployK8s_MatchesMakeLocalUp is the M1 acceptance fixture: the deploy
// dry-run must produce exactly the make-local-up command sequence.
func TestDeployK8s_MatchesMakeLocalUp(t *testing.T) {
	got := dryRun(t, (*engine.Engine).DeployK8s)
	wantLines := []string{
		"docker build -t baseline:dev .",
		"docker build -t baseline-ui:dev ./frontend",
		"docker build -t baseline-postgresql:16-pgvector ./deploy/postgres",
		"docker build --build-arg PATCH_OLLAMA=1 -t baseline-mem0-api:ollama ./deploy/mem0-api",
		"docker save baseline:dev | docker exec -i desktop-control-plane ctr -n k8s.io images import -",
		"docker save baseline-ui:dev | docker exec -i desktop-control-plane ctr -n k8s.io images import -",
		"docker save baseline-postgresql:16-pgvector | docker exec -i desktop-control-plane ctr -n k8s.io images import -",
		"docker save baseline-mem0-api:ollama | docker exec -i desktop-control-plane ctr -n k8s.io images import -",
		"grype baseline:dev",
		"grype baseline-ui:dev",
		"helm repo add bitnami https://charts.bitnami.com/bitnami",
		"helm dependency build deploy/charts/baseline",
	}
	for _, w := range wantLines {
		if !strings.Contains(got, w+"\n") {
			t.Errorf("deploy missing line:\n  %s\n--- full output ---\n%s", w, got)
		}
	}
	// The helm apply line has a dynamic rollmeTimestamp — assert the stable prefix.
	applyPrefix := "helm upgrade --install baseline deploy/charts/baseline --kube-context docker-desktop -n baseline --create-namespace -f deploy/local/values.yaml --set rollmeTimestamp="
	if !strings.Contains(got, applyPrefix) {
		t.Errorf("deploy missing helm apply line with prefix:\n  %s\n--- got ---\n%s", applyPrefix, got)
	}
}

func TestDownK8s(t *testing.T) {
	got := dryRun(t, func(e *engine.Engine) error { return e.DownK8s(false) })
	want := "helm --kube-context docker-desktop -n baseline uninstall baseline"
	if !strings.Contains(got, want) {
		t.Errorf("down missing %q, got:\n%s", want, got)
	}
}

func TestDownK8s_Destroy(t *testing.T) {
	got := dryRun(t, func(e *engine.Engine) error { return e.DownK8s(true) })
	if !strings.Contains(got, "kubectl --context docker-desktop -n baseline delete pvc --all") {
		t.Errorf("down --destroy must drop PVCs, got:\n%s", got)
	}
}

func TestStatusK8s(t *testing.T) {
	got := dryRun(t, (*engine.Engine).StatusK8s)
	if !strings.Contains(got, "kubectl --context docker-desktop -n baseline get pods") {
		t.Errorf("status wrong, got:\n%s", got)
	}
}
