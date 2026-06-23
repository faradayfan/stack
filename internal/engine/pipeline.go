package engine

import "fmt"

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
// A pattern with no `pipeline:` falls back to the default for its type (so older
// configs keep working unchanged).

// verbTerminal maps a forward verb to the pipeline stage it runs up to (and
// including). `deploy` has no fixed terminal — it runs the whole pipeline.
var verbTerminal = map[string]string{
	"check": "check",
	"build": "build",
	// deploy → the last stage (handled in RunPipeline)
}

// defaultPipeline is the built-in stage order for a pattern type when the pattern
// declares none. It preserves the pre-pipeline behavior.
func defaultPipeline(patternType string) []string {
	switch patternType {
	case "k8s":
		return []string{"build", "deliver", "scan", "apply"}
	case "native":
		return []string{"build"}
	}
	return nil
}

// stageActions returns the engine action for each stage name, for the current
// pattern type. A nil entry means "this stage isn't valid for this type".
func (e *Engine) stageActions() map[string]func() error {
	common := map[string]func() error{
		"check": func() error {
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
		},
	}
	switch e.Cfg.Pattern.Type {
	case "k8s":
		common["build"] = e.k8sBuild
		common["deliver"] = e.k8sDeliver
		common["scan"] = e.k8sScan
		common["apply"] = e.k8sApply
		common["wait"] = e.k8sWait
	case "native":
		common["build"] = e.BuildNative
	}
	return common
}

// RunPipeline runs the pattern's pipeline up to and including the verb's terminal
// stage. A failing stage stops the run (so `check` first gates everything after).
func (e *Engine) RunPipeline(verb string) error {
	if err := e.ValidateBindings(); err != nil {
		return err
	}
	pipeline := e.Cfg.Pattern.Pipeline
	if len(pipeline) == 0 {
		pipeline = defaultPipeline(e.Cfg.Pattern.Type)
	}
	if len(pipeline) == 0 {
		return fmt.Errorf("pattern %q (type %q) has no pipeline and no default", e.Cfg.Name, e.Cfg.Pattern.Type)
	}

	// Find the terminal stage index for this verb.
	terminal := len(pipeline) - 1 // deploy → whole pipeline
	if stage, ok := verbTerminal[verb]; ok {
		idx := indexOf(pipeline, stage)
		if idx < 0 {
			return fmt.Errorf("verb %q needs stage %q, which is not in pattern %q's pipeline %v", verb, stage, e.Cfg.Name, pipeline)
		}
		terminal = idx
	}

	actions := e.stageActions()
	for _, stage := range pipeline[:terminal+1] {
		action, ok := actions[stage]
		if !ok || action == nil {
			return fmt.Errorf("pattern %q (type %q): unknown pipeline stage %q", e.Cfg.Name, e.Cfg.Pattern.Type, stage)
		}
		if err := action(); err != nil {
			return fmt.Errorf("stage %q: %w", stage, err)
		}
	}
	return nil
}

func indexOf(ss []string, s string) int {
	for i, v := range ss {
		if v == s {
			return i
		}
	}
	return -1
}
