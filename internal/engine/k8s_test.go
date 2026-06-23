package engine_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/faradayfan/stack/internal/config"
	"github.com/faradayfan/stack/internal/engine"
	"github.com/faradayfan/stack/internal/plugins"
)

// baselineLikeCfg mirrors baseline's .stack k8s pattern (the M1 fixture) so the
// dry-run output can be asserted against the known make-local-up command stream.
func baselineLikeCfg() config.Resolved {
	return config.Resolved{
		App:     "baseline",
		EnvName: "local-k8s",
		Name:    "k8s",
		Pattern: config.Pattern{
			Type:          "k8s",
			KubeContext:   "docker-desktop",
			Namespace:     "baseline",
			ImageDelivery: "load",
			DefaultTag:    "dev",
			Artifacts: map[string]config.Artifact{
				"baseline":            {Context: "."},
				"baseline-ui":         {Context: "./frontend"},
				"baseline-postgresql": {Context: "./deploy/postgres", Tag: "16-pgvector"},
				"baseline-mem0-api":   {Context: "./deploy/mem0-api", Tag: "ollama", Args: map[string]string{"PATCH_OLLAMA": "1"}},
			},
			Steps: map[string]config.StepBlock{
				"build":      {Tool: "docker"},
				"deliver":    {Tool: "docker", Config: map[string]any{"node": "desktop-control-plane"}},
				"scan":       {Tool: "grype", Config: map[string]any{"images": []any{"baseline", "baseline-ui"}, "fail_on": "high"}},
				"wait_ready": {Tool: "helm"},
				"teardown":   {Tool: "helm"},
				"status":     {Tool: "kubectl"},
				"apply": {Tool: "helm", Config: map[string]any{
					"chart":  "deploy/charts/baseline",
					"values": []any{"deploy/local/values.yaml"},
					"set":    map[string]any{"rollmeTimestamp": "{{ now_unix }}"},
					"repos":  []any{map[string]any{"name": "bitnami", "url": "https://charts.bitnami.com/bitnami"}},
				}},
			},
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

// piLikeCfg mirrors a registry-push env (the Pi): push delivery + a platform +
// an env-wide registry/tag. The build must use buildx with --platform and --push.
func piLikeCfg() config.Resolved {
	return config.Resolved{
		App:     "baseline",
		EnvName: "pi",
		Name:    "k8s",
		Pattern: config.Pattern{
			Type:          "k8s",
			KubeContext:   "k3s",
			Namespace:     "baseline",
			ImageDelivery: "push",
			Registry:      "reg.example:5000",
			Platform:      "linux/arm64",
			Tag:           "abc123",
			DefaultTag:    "dev",
			Artifacts: map[string]config.Artifact{
				"baseline": {Context: "."},
			},
			Steps: map[string]config.StepBlock{
				// platform is NOT in the build block — it flows from the pattern's
				// Platform field (declared once on the pattern).
				"build":   {Tool: "docker"},
				"deliver": {Tool: "docker"},
				"scan":    {Tool: "grype", Config: map[string]any{"images": []any{"baseline"}, "fail_on": "high"}},
				"apply":   {Tool: "helm", Config: map[string]any{"chart": "deploy/charts/baseline"}},
			},
		},
	}
}

// TestDeployK8s_PushUsesBuildx: on a push env with a platform, the build must be
// `docker buildx build --platform … --push` (not classic build + docker push).
func TestDeployK8s_PushUsesBuildx(t *testing.T) {
	reg, err := plugins.Load()
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(piLikeCfg(), reg, true)
	var buf bytes.Buffer
	e.Out = &buf
	if err := e.DeployK8s(); err != nil {
		t.Fatalf("dry-run errored: %v", err)
	}
	got := buf.String()
	want := "docker buildx build --platform linux/arm64 -t reg.example:5000/baseline:abc123 --push ."
	if !strings.Contains(got, want+"\n") {
		t.Errorf("push build must use buildx --platform --push:\n  want %s\n--- got ---\n%s", want, got)
	}
}

// TestDeployK8s_SetResolvesGitShortSha: a `set:` value of {{ git_short_sha }}
// must render the actual git SHA (not the literal token), so e.g. image.tag
// matches the tag the build/push step used. Regression: resolveSet only handled
// {{ now_unix }}, leaving {{ git_short_sha }} as a literal → chart fell back to
// its default tag → cluster pulled an image that was never pushed.
func TestDeployK8s_SetResolvesGitShortSha(t *testing.T) {
	reg, err := plugins.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg := piLikeCfg()
	cfg.Pattern.Steps["apply"] = config.StepBlock{Tool: "helm", Config: map[string]any{
		"chart": "deploy/charts/baseline",
		"set":   map[string]any{"image.tag": "{{ git_short_sha }}"},
	}}
	e := engine.New(cfg, reg, true)
	var buf bytes.Buffer
	e.Out = &buf
	if err := e.DeployK8s(); err != nil {
		t.Fatalf("dry-run errored: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "image.tag={{") || strings.Contains(got, "git_short_sha") {
		t.Errorf("set value {{ git_short_sha }} was not resolved:\n%s", got)
	}
	// The build refs use the same resolved tag — image.tag must match one of them.
	if !strings.Contains(got, "--set image.tag=") {
		t.Errorf("expected a resolved --set image.tag=<sha>:\n%s", got)
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
