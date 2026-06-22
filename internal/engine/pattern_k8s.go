package engine

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/faradayfan/stack/internal/config"
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
	p := e.Cfg.Pattern
	envTag := e.envTag()
	images := p.SortedImages() // deterministic order (map → sorted by name)
	scan := p.Scan()
	// The apply step block's config carries helm specifics (chart, values, set, repos).
	apply, _ := e.binding("apply")
	chart, _ := apply.Config["chart"].(string)

	// 1. build-artifact — per image. Identity + the build block's config (platform)
	//    are merged by the engine; the pattern passes only per-image dynamics.
	for _, img := range images {
		if _, err := e.Step("build-artifact", map[string]any{
			"ref":      p.ImageRef(img, envTag),
			"context":  img.Context,
			"args":     img.Args,
			"platform": p.Platform, // pattern setting (buildx --platform)
		}); err != nil {
			return err
		}
	}

	// 2. deliver-artifact — load into the node (or push), one per image. `node`/
	//    `registry` come from the deliver block's config; `delivery` from identity.
	for _, img := range images {
		if _, err := e.Step("deliver-artifact", map[string]any{
			"ref": p.ImageRef(img, envTag),
		}); err != nil {
			return err
		}
	}

	// 3. scan-artifact — first-party images only, threshold from the scan block.
	for _, name := range scan.Images {
		ref, err := imageRefByName(p, name, envTag)
		if err != nil {
			return err
		}
		if _, err := e.Step("scan-artifact", map[string]any{
			"target":  ref,
			"fail_on": scan.FailOn,
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
		"release": e.Cfg.ReleaseName(),
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
	for k, val := range e.Cfg.Pattern.Identity() {
		merged[k] = val
	}
	for k, val := range inputs {
		merged[k] = val
	}
	return render(tmpl, merged)
}

// defaultTool returns the conventional tool for a step of a given pattern TYPE
// when the pattern omits the step block. Lets a minimal pattern still tear down /
// observe without spelling out every step. Explicit blocks always win (see Step).
func defaultTool(patternType, step string) (string, bool) {
	if patternType != "k8s" {
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

func imageRefByName(p config.Pattern, name, envTag string) (string, error) {
	img, ok := p.ImageByName(name)
	if !ok {
		return "", fmt.Errorf("scan image %q not found in pattern images", name)
	}
	return p.ImageRef(img, envTag), nil
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
