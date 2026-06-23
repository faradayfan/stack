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

// Down tears down via the pattern's `teardown` step block (e.g. helm uninstall).
// With destroy, it also runs kubectl's teardown variant to drop volumes — the one
// remaining tool-specific bit, rendered explicitly so dry-run is complete.
func (e *Engine) Down(destroy bool) error {
	if _, err := e.Step("teardown", map[string]any{
		"release": e.Cfg.ReleaseName(),
	}); err != nil {
		return err
	}
	if destroy {
		cmd, err := e.renderTool("kubectl", "teardown", nil)
		if err != nil {
			return err
		}
		return e.RunRaw(cmd)
	}
	return nil
}

// Status runs the pattern's `status` step block (e.g. kubectl get pods).
func (e *Engine) Status() error {
	_, err := e.Step("status", nil)
	return err
}

// renderTool renders a specific tool's step command without it being the bound
// tool for that step (used by down --destroy → kubectl). Identity is merged in.
func (e *Engine) renderTool(tool, step string, inputs map[string]any) (string, error) {
	m, ok := e.Plugins.Get(tool)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", tool)
	}
	v, err := e.detect(m)
	if err != nil {
		return "", err
	}
	tmpl, ok, err := m.CommandFor(step, v)
	if err != nil || !ok {
		return "", fmt.Errorf("tool %q does not provide step %q", tool, step)
	}
	merged := map[string]any{}
	for k, val := range e.Cfg.Pattern.Identity() {
		merged[k] = val
	}
	for k, val := range inputs {
		merged[k] = val
	}
	return render(tmpl, merged)
}
