package engine_test

import (
	"strings"
	"testing"

	"github.com/faradayfan/stack/internal/config"
	"github.com/faradayfan/stack/internal/engine"
	"github.com/faradayfan/stack/internal/plugins"
)

// setupEngine builds an engine for the setup flow: tools_manager is app-global,
// checks are pattern-scoped (NewForPattern reads both off the app + pattern).
func setupEngine(t *testing.T, toolsManager string, checks map[string]config.Check) *engine.Engine {
	t.Helper()
	reg, err := plugins.Load()
	if err != nil {
		t.Fatal(err)
	}
	app := config.App{Name: "x", ToolsManager: toolsManager}
	pat := config.Pattern{Type: "k8s", Checks: checks}
	return engine.NewForPattern(app, "k8s", pat, reg, false)
}

// TestSetup_ToolsResolveByMethod: the doctor classifies each tool by its setup
// method (asdf / unmanaged / manual / needs-manager) without installing.
func TestSetup_DoctorClassifiesMethods(t *testing.T) {
	e := setupEngine(t, "asdf", map[string]config.Check{
		"lint": {Tool: "golangci"}, // asdf (plugin golangci-lint)
		"sast": {Tool: "gosec"},    // unmanaged (go install)
		"fmt":  {Tool: "gofmt"},    // asdf (golang)
	})
	results, _, err := e.Setup(true) // doctor
	if err != nil {
		t.Fatal(err)
	}
	byTool := map[string]engine.SetupResult{}
	for _, r := range results {
		byTool[r.Tool] = r
	}
	if byTool["golangci"].Method != "asdf" {
		t.Errorf("golangci method = %q, want asdf", byTool["golangci"].Method)
	}
	if byTool["gosec"].Method != "unmanaged" {
		t.Errorf("gosec method = %q, want unmanaged", byTool["gosec"].Method)
	}
	if len(results) != 3 {
		t.Errorf("want 3 tool results, got %d", len(results))
	}
}

// TestSetup_AsdfToolNeedsManager: an asdf-managed tool with NO tools_manager set
// is reported as needing a manager (not installed, not fatal).
func TestSetup_AsdfToolNeedsManager(t *testing.T) {
	e := setupEngine(t, "", map[string]config.Check{"lint": {Tool: "golangci"}}) // no tools_manager
	results, ok, err := e.Setup(true)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("setup should not be satisfied when a managed tool has no manager")
	}
	if !strings.Contains(results[0].Action, "tools_manager") {
		t.Errorf("expected a 'needs tools_manager' action, got %q", results[0].Action)
	}
}

// TestSetup_UnknownManager errors clearly.
func TestSetup_UnknownManager(t *testing.T) {
	e := setupEngine(t, "nope", map[string]config.Check{"lint": {Tool: "golangci"}})
	if _, _, err := e.Setup(true); err == nil {
		t.Error("expected error for unknown tools_manager")
	}
}

// TestSetupSummary_SingleLinePerTool: a tool whose detect output is multi-line
// (e.g. gosec's "Version:\nGit tag:\n…") must not corrupt the summary — each
// tool is exactly one line.
func TestSetupSummary_SingleLinePerTool(t *testing.T) {
	rs := []engine.SetupResult{
		{Tool: "gosec", Method: "unmanaged", Present: true,
			Have: "Version: dev\nGit tag: \nBuild date:", Want: "2.22.5"},
	}
	s := engine.SetupSummary(rs)
	body := strings.TrimPrefix(s, "\nsetup:\n")
	body = strings.TrimRight(body, "\n")
	if strings.Count(body, "\n") != 0 {
		t.Errorf("summary for one tool must be one line, got:\n%q", body)
	}
}

func TestSetupSummary(t *testing.T) {
	rs := []engine.SetupResult{
		{Tool: "golangci", Method: "asdf", Present: true, Have: "2.12.2", Want: "2.12.2", Action: "ok"},
		{Tool: "gosec", Method: "unmanaged", Present: false, Want: "2.22.5", Action: "install: go install …"},
	}
	s := engine.SetupSummary(rs)
	if !strings.Contains(s, "golangci") || !strings.Contains(s, "have=2.12.2") {
		t.Errorf("summary missing golangci details:\n%s", s)
	}
	if !strings.Contains(s, "miss") || !strings.Contains(s, "gosec") {
		t.Errorf("summary should mark gosec missing:\n%s", s)
	}
}
