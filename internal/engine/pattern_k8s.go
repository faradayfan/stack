package engine

import "fmt"

// envTag returns the pattern's image tag with tokens resolved (e.g. a git sha for
// the Pi). Empty when the pattern declares no tag.
func (e *Engine) envTag() string {
	if e.Cfg.Pattern.Tag == "" {
		return ""
	}
	return resolveToken(e.Cfg.Pattern.Tag)
}

// Down runs the pattern's `teardown` step block (e.g. helm uninstall). With
// destroy, it also runs the pattern's `destroy` step block (e.g. kubectl drop
// PVCs) — both via the generic Step path, so the engine names no tool. A
// `--destroy` with no `destroy:` block declared is an error (the pattern didn't
// wire volume cleanup), not a silent no-op.
func (e *Engine) Down(destroy bool) error {
	if _, err := e.Step("teardown", map[string]any{
		"release": e.Cfg.ReleaseName(),
	}); err != nil {
		return err
	}
	if destroy {
		if _, ok := e.block("destroy"); !ok {
			return fmt.Errorf("--destroy: pattern %q declares no `destroy` step (volume cleanup)", e.Cfg.Name)
		}
		if _, err := e.Step("destroy", map[string]any{
			"release": e.Cfg.ReleaseName(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// Status runs the pattern's `status` step block (e.g. kubectl get pods).
func (e *Engine) Status() error {
	_, err := e.Step("status", nil)
	return err
}
