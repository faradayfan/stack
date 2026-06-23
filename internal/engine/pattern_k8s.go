package engine

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// resolveToken expands the supported runtime template tokens in a value:
// {{ now_unix }} and {{ git_short_sha }}. Non-token values pass through.
func resolveToken(v string) string {
	switch strings.TrimSpace(v) {
	case "{{ now_unix }}", "{{now_unix}}":
		return strconv.FormatInt(time.Now().Unix(), 10)
	case "{{ git_short_sha }}", "{{git_short_sha}}":
		out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
		if err != nil {
			return "unknown"
		}
		return strings.TrimSpace(string(out))
	default:
		return v
	}
}

// envTag returns the pattern's image tag with tokens resolved (e.g. a git sha for
// the Pi). Empty when the pattern declares no tag.
func (e *Engine) envTag() string {
	if e.Cfg.Pattern.Tag == "" {
		return ""
	}
	return resolveToken(e.Cfg.Pattern.Tag)
}

// k8sApply runs helm upgrade --install. chart/values/set/repos come from the
// apply step block; the repo-add + dependency-build preamble lives in the helm
// manifest's `pre:` (so the engine has no helm-specific glue). `set` tokens
// ({{ now_unix }}, {{ git_short_sha }}) are resolved here before rendering.
func (e *Engine) k8sApply() error {
	apply, _ := e.binding("apply")
	set, err := resolveSet(apply.Config["set"])
	if err != nil {
		return err
	}
	_, err = e.Step("apply", map[string]any{
		"release": e.Cfg.ReleaseName(),
		"set":     set, // resolved (overrides the raw config.set)
	})
	return err
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

// resolveSet expands template tokens in the apply binding's `set` config. It
// supports the same tokens as the rest of the engine ({{ now_unix }} and
// {{ git_short_sha }}) via resolveToken, so e.g. `image.tag: "{{ git_short_sha }}"`
// matches the tag the build/push step used. The raw value is a map[string]any.
func resolveSet(raw any) (map[string]string, error) {
	out := map[string]string{}
	m, ok := raw.(map[string]any)
	if !ok {
		return out, nil // no set block
	}
	for k, v := range m {
		out[k] = resolveToken(fmt.Sprint(v))
	}
	return out, nil
}
