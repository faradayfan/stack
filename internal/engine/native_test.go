package engine_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/faradayfan/stack/internal/config"
	"github.com/faradayfan/stack/internal/engine"
	"github.com/faradayfan/stack/internal/plugins"
)

// TestBuildNative: a native pattern with a go build block renders `go build -o
// <output> <package>` per artifact (sorted), and nothing else (build-only).
func TestBuildNative(t *testing.T) {
	cfg := config.Resolved{
		App:  "stack",
		Name: "local",
		Pattern: config.Pattern{
			Pipeline: []string{"build"},
			Artifacts: map[string]config.Artifact{
				"stack": {Package: "./cmd/stack", Output: "bin/stack"},
			},
			Steps: map[string]config.StepBlock{
				"build": {Tool: "go"},
			},
		},
	}
	reg, err := plugins.Load()
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(cfg, reg, true)
	var buf bytes.Buffer
	e.Out = &buf
	if err := e.RunPipeline("build"); err != nil {
		t.Fatalf("BuildNative errored: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	want := "go build -o bin/stack ./cmd/stack"
	if got != want {
		t.Errorf("native build:\n got  %q\n want %q", got, want)
	}
}

// TestBuildNative_Ldflags: an artifact's ldflags reach the command.
func TestBuildNative_Ldflags(t *testing.T) {
	cfg := config.Resolved{
		App:  "stack",
		Name: "local",
		Pattern: config.Pattern{
			Pipeline: []string{"build"},
			Artifacts: map[string]config.Artifact{
				"stack": {Package: "./cmd/stack", Output: "bin/stack", Ldflags: "-s -w"},
			},
			Steps: map[string]config.StepBlock{"build": {Tool: "go"}},
		},
	}
	reg, _ := plugins.Load()
	e := engine.New(cfg, reg, true)
	var buf bytes.Buffer
	e.Out = &buf
	if err := e.RunPipeline("build"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "-ldflags '-s -w'") {
		t.Errorf("ldflags not rendered:\n%s", buf.String())
	}
}
