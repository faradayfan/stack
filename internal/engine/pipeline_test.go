package engine_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/faradayfan/stack/internal/config"
	"github.com/faradayfan/stack/internal/engine"
	"github.com/faradayfan/stack/internal/plugins"
)

// nativeWithPipeline builds a native pattern with a [check, build] pipeline.
func nativeWithPipeline(pipeline []string) config.Resolved {
	return config.Resolved{
		App:  "stack",
		Name: "local",
		Pattern: config.Pattern{
			Pipeline: pipeline,
			Artifacts: map[string]config.Artifact{
				"stack": {Package: "./cmd/stack", Output: "bin/stack"},
			},
			Steps:  map[string]config.StepBlock{"build": {Tool: "go"}},
			Checks: map[string]config.Check{"format": {Tool: "gofmt"}},
		},
	}
}

func runVerb(t *testing.T, cfg config.Resolved, verb string) (string, error) {
	t.Helper()
	reg, err := plugins.Load()
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(cfg, reg, true) // dry-run
	var buf bytes.Buffer
	e.Out = &buf
	err = e.RunPipeline(verb)
	return buf.String(), err
}

// TestPipeline_BuildRunsCheckFirst: with pipeline [check, build], `stack build`
// runs the check stage before the build stage (gating by list order).
func TestPipeline_BuildRunsCheckFirst(t *testing.T) {
	out, err := runVerb(t, nativeWithPipeline([]string{"check", "build"}), "build")
	if err != nil {
		t.Fatalf("build pipeline errored: %v", err)
	}
	ci := strings.Index(out, "gofmt")                 // the check stage
	bi := strings.Index(out, "go build -o bin/stack") // the build stage
	if ci < 0 || bi < 0 {
		t.Fatalf("expected both check and build to run:\n%s", out)
	}
	if ci > bi {
		t.Errorf("check must run BEFORE build:\n%s", out)
	}
}

// TestPipeline_CheckTerminal: `stack build` with pipeline [check, build] stops
// after build; `check` verb stops after check (does not build).
func TestPipeline_CheckVerbStopsAtCheck(t *testing.T) {
	out, err := runVerb(t, nativeWithPipeline([]string{"check", "build"}), "check")
	if err != nil {
		t.Fatalf("check verb errored: %v", err)
	}
	if strings.Contains(out, "go build -o bin/stack") {
		t.Errorf("check verb must NOT run the build stage:\n%s", out)
	}
	if !strings.Contains(out, "gofmt") {
		t.Errorf("check verb should run the check stage:\n%s", out)
	}
}

// TestPipeline_RequiredWhenUnset: a pattern with no pipeline is a clear error
// (there is no `type` to imply a default — a pattern IS its pipeline).
func TestPipeline_RequiredWhenUnset(t *testing.T) {
	_, err := runVerb(t, nativeWithPipeline(nil), "build")
	if err == nil {
		t.Error("expected an error: pattern declares no pipeline")
	}
}

// TestPipeline_VerbStageNotInPipeline: asking for `build` when the pipeline has
// no build stage is a clear error.
func TestPipeline_VerbStageNotInPipeline(t *testing.T) {
	_, err := runVerb(t, nativeWithPipeline([]string{"check"}), "build")
	if err == nil {
		t.Error("expected an error: build verb but no build stage in pipeline")
	}
}
