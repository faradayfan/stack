package engine

import (
	"fmt"

	"github.com/faradayfan/stack/internal/config"
)

// The pipeline is the ordered list of fine-grained STAGES a pattern runs
// (check, build, scan, deliver, apply, wait). A forward VERB runs the pipeline
// up to and including its terminal stage, so list order is gating order — put
// `check` first and everything after is gated by it.
//
//	pipeline: [check, build, scan, deliver, apply, wait]
//	  stack check  → check
//	  stack build  → check, build
//	  stack deploy → check, build, scan, deliver, apply, wait
//
// Each stage just runs its step block's TOOL for the matching abstract step —
// the engine has no per-pattern-type code. `build` runs whatever tool the
// `build:` block names (go OR docker); the manifest knows how. There is no
// `type` field: a pattern IS its pipeline + step blocks.

// loopKind is how a stage iterates.
type loopKind int

const (
	loopOnce        loopKind = iota // run the step once
	loopPerArtifact                 // run once per artifact in `artifacts:`
	loopScanImages                  // run once per image named in the scan block
)

// stageDef maps a pipeline stage to the abstract step its tool performs and how
// it loops. This is the engine's fixed vocabulary — the same for every pattern.
type stageDef struct {
	abstract string
	loop     loopKind
}

var stageDefs = map[string]stageDef{
	"build":   {"build-artifact", loopPerArtifact},
	"deliver": {"deliver-artifact", loopPerArtifact},
	"scan":    {"scan-artifact", loopScanImages},
	"apply":   {"apply", loopOnce},
	"wait":    {"wait-ready", loopOnce},
	// "check" is special (runs the check flow, not a tool step) — see runStage.
}

// verbTerminal maps a forward verb to the pipeline stage it runs up to (and
// including). `deploy` has no fixed terminal — it runs the whole pipeline.
var verbTerminal = map[string]string{
	"check": "check",
	"build": "build",
}

// RunPipeline runs the pattern's pipeline up to and including the verb's terminal
// stage. A failing stage stops the run (so `check` first gates everything after).
func (e *Engine) RunPipeline(verb string) error {
	if err := e.ValidateBindings(); err != nil {
		return err
	}
	pipeline := e.Cfg.Pattern.Pipeline
	if len(pipeline) == 0 {
		return fmt.Errorf("pattern %q declares no pipeline", e.Cfg.Name)
	}

	terminal := len(pipeline) - 1 // deploy → whole pipeline
	if stage, ok := verbTerminal[verb]; ok {
		idx := indexOf(pipeline, stage)
		if idx < 0 {
			return fmt.Errorf("verb %q needs stage %q, not in pattern %q's pipeline %v", verb, stage, e.Cfg.Name, pipeline)
		}
		terminal = idx
	}

	for _, stage := range pipeline[:terminal+1] {
		if err := e.runStage(stage); err != nil {
			return fmt.Errorf("stage %q: %w", stage, err)
		}
	}
	return nil
}

// runStage runs one pipeline stage generically: the `check` stage runs the check
// flow; every other stage runs its step block's tool for the matching abstract
// step, looping per its kind. The per-iteration inputs are the artifact's fields
// plus a computed image `ref`; manifests ignore inputs they don't use
// (missingkey=zero), so the same bag serves docker (ref/context) and go
// (package/output).
func (e *Engine) runStage(stage string) error {
	if stage == "check" {
		results, passed, err := e.Check(nil)
		if e.Out != nil {
			fmt.Fprint(e.Out, Summary(results))
		}
		if err != nil {
			return err
		}
		if !passed {
			return fmt.Errorf("checks failed")
		}
		return nil
	}

	def, ok := stageDefs[stage]
	if !ok {
		return fmt.Errorf("unknown pipeline stage %q", stage)
	}
	p := e.Cfg.Pattern
	envTag := e.envTag()

	switch def.loop {
	case loopOnce:
		// once-stages (apply, wait) pass only the release; the step block's config
		// (chart/values/set/repos for apply) flows through stepInputs, where its
		// tokens are resolved and the helm manifest's `pre:` adds the preamble. No
		// per-stage special case.
		_, err := e.Step(def.abstract, map[string]any{"release": e.Cfg.ReleaseName()})
		return err
	case loopPerArtifact:
		for _, a := range p.SortedArtifacts() {
			if _, err := e.Step(def.abstract, artifactInputs(p, a, envTag)); err != nil {
				return err
			}
		}
		return nil
	case loopScanImages:
		for _, name := range p.Scan().Images {
			a, ok := p.ArtifactByName(name)
			if !ok {
				return fmt.Errorf("scan image %q not found in pattern artifacts", name)
			}
			if _, err := e.Step(def.abstract, map[string]any{
				"target":  p.ImageRef(a, envTag),
				"fail_on": p.Scan().FailOn,
			}); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("stage %q: unhandled loop kind", stage)
}

// artifactInputs is the generic per-artifact input bag for a build/deliver stage.
// It carries BOTH the image fields (ref/context/args/platform) and the binary
// fields (package/output/ldflags); the bound tool's manifest template uses only
// the ones it needs (missingkey=zero), so docker and go share one code path.
func artifactInputs(p config.Pattern, a config.Artifact, envTag string) map[string]any {
	return map[string]any{
		"ref":      p.ImageRef(a, envTag),
		"context":  a.Context,
		"args":     a.Args,
		"platform": p.Platform,
		"package":  a.Package,
		"output":   a.Output,
		"ldflags":  a.Ldflags,
	}
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}
