package engine

import (
	"fmt"
	"strconv"
	"time"

	"github.com/faradayfan/stack/internal/config"
)

// DeployK8s runs the k8s pattern deploy sequence:
//
//	build-artifact (each image) → deliver-artifact (each) → scan-artifact (each
//	scan image) → [helm repo add + dependency build] → apply
//
// It produces exactly the command stream the M1 worked example specifies.
func (e *Engine) DeployK8s() error {
	if err := e.ValidateBindings(); err != nil {
		return err
	}
	c := e.Cfg
	// The apply binding's config carries helm specifics (chart, values, set, repos).
	apply, _ := e.binding("apply")
	chart, _ := apply.Config["chart"].(string)

	// 1. build-artifact — per image. Identity + the build tool's config (platform)
	//    are merged by the engine; the pattern passes only per-image dynamics.
	for _, img := range c.App.Images {
		if _, err := e.Step("build-artifact", map[string]any{
			"ref":     c.ImageRef(img),
			"context": img.Context,
			"args":    img.Args,
		}); err != nil {
			return err
		}
	}

	// 2. deliver-artifact — load into the node (or push), one per image. `node`/
	//    `registry` come from the deliver binding's config; `delivery` from env.
	for _, img := range c.App.Images {
		if _, err := e.Step("deliver-artifact", map[string]any{
			"ref": c.ImageRef(img),
		}); err != nil {
			return err
		}
	}

	// 3. scan-artifact — first-party images only, threshold from .grype.yaml.
	for _, name := range c.App.Scan.Images {
		ref, err := imageRefByName(c, name)
		if err != nil {
			return err
		}
		if _, err := e.Step("scan-artifact", map[string]any{
			"target":  ref,
			"fail_on": c.App.Scan.FailOn,
		}); err != nil {
			return err
		}
	}

	// 4. chart deps (helm repo add + dependency build) — from the apply binding's
	//    `repos` config; engine-level preamble.
	repos := toRepos(apply.Config["repos"])
	for _, r := range repos {
		if err := e.RunRaw(fmt.Sprintf("helm repo add %s %s", r.Name, r.URL)); err != nil {
			return err
		}
	}
	if chart != "" && len(repos) > 0 {
		if err := e.RunRaw("helm dependency build " + chart); err != nil {
			return err
		}
	}

	// 5. apply (helm upgrade --install). chart/values/set come from the binding
	//    config (merged by the engine); `set` tokens resolved here; release added.
	set, err := resolveSet(apply.Config["set"])
	if err != nil {
		return err
	}
	_, err = e.Step("apply", map[string]any{
		"release": c.ReleaseName(),
		"set":     set, // resolved (overrides the raw config.set)
	})
	return err
}

// DownK8s tears down: helm uninstall, and (with destroy) drop PVCs via kubectl.
func (e *Engine) DownK8s(destroy bool) error {
	// release is dynamic; kube_context/namespace come from env identity (merged).
	if _, err := e.Step("teardown", map[string]any{
		"release": e.Cfg.ReleaseName(),
	}); err != nil {
		return err
	}
	if destroy {
		// PVC deletion is kubectl's teardown variant; render it explicitly even
		// though the env's `teardown` step is helm's. Identity merged in.
		cmd, err := e.renderTool("kubectl", "teardown", nil)
		if err != nil {
			return err
		}
		return e.RunRaw(cmd)
	}
	return nil
}

// StatusK8s shows the namespace's pods. (kube_context/namespace from identity.)
func (e *Engine) StatusK8s() error {
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
	for k, val := range e.Cfg.Env.Identity() {
		merged[k] = val
	}
	for k, val := range inputs {
		merged[k] = val
	}
	return render(tmpl, merged)
}

// defaultTool returns the conventional tool for a step in a pattern when the
// env context omits the binding. Lets a minimal context still tear down/observe
// without spelling out every step. Explicit bindings always win (see Step).
func defaultTool(pattern, step string) (string, bool) {
	if pattern != "k8s" {
		return "", false
	}
	switch step {
	case "build-artifact", "deliver-artifact":
		return "docker", true
	case "scan-artifact":
		return "grype", true
	case "render-config", "apply", "wait-ready", "teardown":
		return "helm", true
	case "status", "logs":
		return "kubectl", true
	}
	return "", false
}

func imageRefByName(c config.Merged, name string) (string, error) {
	for _, img := range c.App.Images {
		if img.Name == name {
			return c.ImageRef(img), nil
		}
	}
	return "", fmt.Errorf("scan image %q not found in app.images", name)
}

// resolveSet expands template tokens in the apply binding's `set` config
// ({{ now_unix }}). The raw value is a map[string]any from YAML.
func resolveSet(raw any) (map[string]string, error) {
	out := map[string]string{}
	m, ok := raw.(map[string]any)
	if !ok {
		return out, nil // no set block
	}
	for k, v := range m {
		s := fmt.Sprint(v)
		switch s {
		case "{{ now_unix }}", "{{now_unix}}":
			out[k] = strconv.FormatInt(time.Now().Unix(), 10)
		default:
			out[k] = s
		}
	}
	return out, nil
}

// toRepos parses the apply binding's `repos` config (a list of {name,url}).
func toRepos(raw any) []config.HelmRepo {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	var out []config.HelmRepo
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		url, _ := m["url"].(string)
		if name != "" && url != "" {
			out = append(out, config.HelmRepo{Name: name, URL: url})
		}
	}
	return out
}
