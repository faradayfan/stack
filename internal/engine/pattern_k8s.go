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
	c := e.Cfg

	// 1. build-artifact — one per image.
	for _, img := range c.App.Images {
		if _, err := e.Step("build-artifact", map[string]any{
			"ref":     c.ImageRef(img),
			"context": img.Context,
			"args":    img.Args,
		}); err != nil {
			return err
		}
	}

	// 2. deliver-artifact — load into the node (or push), one per image.
	for _, img := range c.App.Images {
		if _, err := e.Step("deliver-artifact", map[string]any{
			"ref":      c.ImageRef(img),
			"delivery": deliveryOrDefault(c.Env.ImageDelivery),
			"node":     c.Env.Node,
			"registry": c.Env.Registry,
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

	// 4. chart deps (helm repo add + dependency build) — engine-level preamble.
	for _, repo := range c.Env.Deps.HelmRepos {
		if err := e.RunRaw(fmt.Sprintf("helm repo add %s %s", repo.Name, repo.URL)); err != nil {
			return err
		}
	}
	if c.Env.Chart != "" && len(c.Env.Deps.HelmRepos) > 0 {
		if err := e.RunRaw("helm dependency build " + c.Env.Chart); err != nil {
			return err
		}
	}

	// 5. apply (helm upgrade --install) with resolved --set values.
	set, err := resolveSet(c.Env.HelmSet)
	if err != nil {
		return err
	}
	_, err = e.Step("apply", map[string]any{
		"release":      c.ReleaseName(),
		"chart":        c.Env.Chart,
		"kube_context": c.Env.KubeContext,
		"namespace":    c.Env.Namespace,
		"values":       c.Env.Values,
		"set":          set,
	})
	return err
}

// DownK8s tears down: helm uninstall, and (with destroy) drop PVCs via kubectl.
func (e *Engine) DownK8s(destroy bool) error {
	c := e.Cfg
	if _, err := e.Step("teardown", map[string]any{
		"release":      c.ReleaseName(),
		"kube_context": c.Env.KubeContext,
		"namespace":    c.Env.Namespace,
	}); err != nil {
		return err
	}
	if destroy {
		// PVC deletion is kubectl's teardown variant; bind it explicitly so the
		// command renders even though the env's `teardown` step is helm's.
		if cmd, err := e.renderTool("kubectl", "teardown", map[string]any{
			"kube_context": c.Env.KubeContext,
			"namespace":    c.Env.Namespace,
		}); err != nil {
			return err
		} else {
			return e.RunRaw(cmd)
		}
	}
	return nil
}

// StatusK8s shows the namespace's pods.
func (e *Engine) StatusK8s() error {
	_, err := e.Step("status", map[string]any{
		"kube_context": e.Cfg.Env.KubeContext,
		"namespace":    e.Cfg.Env.Namespace,
	})
	return err
}

// renderTool renders a specific tool's step command without it being the bound
// tool for that step (used by down --destroy → kubectl).
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
	return render(tmpl, inputs)
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

func deliveryOrDefault(d string) string {
	if d == "" {
		return "load"
	}
	return d
}

func imageRefByName(c config.Merged, name string) (string, error) {
	for _, img := range c.App.Images {
		if img.Name == name {
			return c.ImageRef(img), nil
		}
	}
	return "", fmt.Errorf("scan image %q not found in app.images", name)
}

// resolveSet expands template tokens in helm_set values ({{ now_unix }}).
func resolveSet(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range in {
		switch v {
		case "{{ now_unix }}", "{{now_unix}}":
			out[k] = strconv.FormatInt(time.Now().Unix(), 10)
		default:
			out[k] = v
		}
	}
	return out, nil
}
