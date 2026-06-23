// Package engine resolves an abstract step to its bound tool, renders the tool's
// command template with the step inputs, and runs it (or prints it under
// dry-run). The engine is a version-aware template renderer + sequencer — all
// tool knowledge lives in the plugin manifests (internal/plugins).
package engine

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/template"

	"github.com/faradayfan/stack/internal/config"
	"github.com/faradayfan/stack/internal/plugins"
)

// Engine carries the resolved pattern, the plugin registry, and run options.
type Engine struct {
	Cfg      config.Resolved
	Plugins  *plugins.Registry
	DryRun   bool
	Out      io.Writer         // where dry-run lines / progress go (default os.Stdout)
	versions map[string]string // tool name → detected version (cache)
}

// New builds an engine for a resolved pattern.
func New(cfg config.Resolved, reg *plugins.Registry, dryRun bool) *Engine {
	return &Engine{Cfg: cfg, Plugins: reg, DryRun: dryRun, Out: os.Stdout, versions: map[string]string{}}
}

// NewForPattern builds an engine for the env-independent check/setup flow, from a
// pattern selected straight off app.yaml (no env merge).
func NewForPattern(app config.App, patName string, pat config.Pattern, reg *plugins.Registry, dryRun bool) *Engine {
	return New(config.Resolved{
		App:          app.Name,
		ToolsManager: app.ToolsManager,
		Name:         patName,
		Pattern:      pat,
	}, reg, dryRun)
}

// stepKey maps an engine ABSTRACT step (the vocabulary plugins provide via
// CommandFor) to the pattern's short step-block key (build/deliver/scan/…). The
// pattern's blocks carry the tool + per-step config for each.
var stepKey = map[string]string{
	"build-artifact":   "build",
	"deliver-artifact": "deliver",
	"scan-artifact":    "scan",
	"render-config":    "render",
	"apply":            "apply",
	"wait-ready":       "wait_ready",
	"teardown":         "teardown",
	"status":           "status",
	"logs":             "logs",
}

// Step resolves the abstract step to its bound tool (from the pattern's step
// block, or a type default when the pattern omits it), renders the command with
// `inputs`, and runs it. Returns the rendered command (for tests/fixtures) even
// under dry-run.
//
// Template inputs compose (lowest→highest precedence): pattern IDENTITY
// (kube_context, namespace, delivery) < the step block's CONFIG < the caller's
// dynamic `inputs` (e.g. per-image `ref`).
func (e *Engine) Step(step string, inputs map[string]any) (string, error) {
	block, ok := e.block(step)
	if !ok {
		return "", fmt.Errorf("no tool bound for step %q in pattern %q (and no type default)", step, e.Cfg.Name)
	}
	m, ok := e.Plugins.Get(block.Tool)
	if !ok {
		return "", fmt.Errorf("step %q bound to unknown tool %q", step, block.Tool)
	}
	version, err := e.detect(m)
	if err != nil {
		return "", err
	}
	def, ok, err := m.StepFor(step, version)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool %q (v%s) does not provide step %q", block.Tool, version, step)
	}
	si := e.stepInputs(block, inputs)
	// pre-commands (e.g. helm repo add / dependency build) run before the main one.
	if err := e.runPre(def.Pre, step, block.Tool, si); err != nil {
		return "", err
	}
	cmd, err := render(def.Command, si)
	if err != nil {
		return "", fmt.Errorf("render %q for step %q: %w", block.Tool, step, err)
	}
	if err := e.run(cmd); err != nil {
		return cmd, err
	}
	return cmd, nil
}

// runPre renders and runs a step's pre-commands. A PreStep with `for: <coll>`
// runs once per item in that engine input collection (each item's fields merged
// in as inputs); without `for`, it runs once. Empty/whitespace renders are
// skipped (a conditional template that produced nothing).
func (e *Engine) runPre(pre []plugins.PreStep, step, tool string, inputs map[string]any) error {
	for _, p := range pre {
		items := []map[string]any{nil} // run-once by default
		if p.For != "" {
			items = collectionItems(inputs[p.For])
		}
		for _, item := range items {
			in := inputs
			if item != nil {
				in = map[string]any{}
				for k, v := range inputs {
					in[k] = v
				}
				for k, v := range item {
					in[k] = v
				}
			}
			cmd, err := render(p.Command, in)
			if err != nil {
				return fmt.Errorf("render pre for %q step %q: %w", tool, step, err)
			}
			if cmd == "" {
				continue
			}
			if err := e.run(cmd); err != nil {
				return err
			}
		}
	}
	return nil
}

// collectionItems normalizes a `for:` collection (a []any of maps from YAML, or a
// []map) into a slice of string-keyed maps.
func collectionItems(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		switch m := it.(type) {
		case map[string]any:
			out = append(out, m)
		case map[any]any:
			conv := map[string]any{}
			for k, val := range m {
				if ks, ok := k.(string); ok {
					conv[ks] = val
				}
			}
			out = append(out, conv)
		}
	}
	return out
}

// ValidateBindings checks each pattern step block's config against its manifest's
// declared config schema (no unknown keys). Run before a flow so a typo'd config
// key fails up front, not at render time.
func (e *Engine) ValidateBindings() error {
	for abstract := range stepKey {
		block, ok := e.block(abstract)
		if !ok {
			continue
		}
		m, ok := e.Plugins.Get(block.Tool)
		if !ok {
			return fmt.Errorf("step %q: unknown tool %q", abstract, block.Tool)
		}
		if err := m.ValidateConfig(block.Config); err != nil {
			return fmt.Errorf("step %q: %w", abstract, err)
		}
	}
	return nil
}

// stepInputs composes pattern identity + the step block's config + dynamic inputs.
// Runtime tokens ({{ now_unix }}, {{ git_short_sha }}) are resolved in the config
// values here — the single choke point — so EVERY tool's config gets token
// resolution uniformly, with no per-stage special case. Dynamic inputs are
// engine-computed (already resolved) and pass through as-is.
func (e *Engine) stepInputs(b config.StepBlock, dynamic map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range e.Cfg.Pattern.Identity() {
		out[k] = v
	}
	for k, v := range b.Config {
		out[k] = resolveTokensDeep(v)
	}
	for k, v := range dynamic {
		out[k] = v
	}
	return out
}

// block returns the resolved step block for an abstract step from the pattern's
// explicit step blocks. There are no type defaults — a pattern declares the tool
// for every step it runs (no magic; you always know what runs).
func (e *Engine) block(abstract string) (config.StepBlock, bool) {
	key, ok := stepKey[abstract]
	if !ok {
		return config.StepBlock{}, false
	}
	if b, ok := e.Cfg.Pattern.Step(key); ok && b.Tool != "" {
		return b, true
	}
	return config.StepBlock{}, false
}

// binding is the old name kept for the pattern_k8s apply-config reader.
func (e *Engine) binding(step string) (config.StepBlock, bool) { return e.block(step) }

// RunRaw runs (or prints) a literal command not driven by a tool manifest —
// for engine-level glue like `helm repo add` / `helm dependency build` that
// belongs to a step's preamble. Kept explicit so the dry-run output is complete.
func (e *Engine) RunRaw(cmd string) error { return e.run(cmd) }

// detect runs the tool's `detect` command at the repo root.
func (e *Engine) detect(m plugins.Manifest) (string, error) { return e.detectIn(m, "") }

// detectIn runs the tool's `detect` command from `dir` (once per tool+dir,
// cached) to read its version. A tool's version may depend on the working
// directory (e.g. an asdf-pinned node/pnpm only resolves inside the subdir that
// pins it), so detection must honor a check's `dir`. Under dry-run, an absent
// tool yields a sentinel so command selection can still render for printing.
func (e *Engine) detectIn(m plugins.Manifest, dir string) (string, error) {
	key := m.Name() + "\x00" + dir
	if v, ok := e.versions[key]; ok {
		return v, nil
	}
	if m.Detect == "" {
		e.versions[key] = "0.0.0"
		return "0.0.0", nil
	}
	cmd := exec.Command("sh", "-c", m.Detect)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		if e.DryRun {
			// Tool may not be installed on the box authoring a dry-run; assume a
			// modern version so the highest variant renders for inspection.
			e.versions[key] = "9999.0.0"
			return "9999.0.0", nil
		}
		return "", fmt.Errorf("detect %s version (%q): %w", m.Name(), m.Detect, err)
	}
	v := strings.TrimSpace(string(out))
	e.versions[key] = v
	return v, nil
}

// run executes a shell command, or prints it under dry-run.
func (e *Engine) run(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if e.DryRun {
		_, _ = fmt.Fprintln(e.Out, cmd)
		return nil
	}
	_, _ = fmt.Fprintln(e.Out, "+ "+cmd)
	c := exec.Command("sh", "-c", cmd)
	c.Stdout, c.Stderr = e.Out, os.Stderr
	return c.Run()
}

// render expands a text/template command with the given inputs.
func render(tmpl string, inputs map[string]any) (string, error) {
	t, err := template.New("cmd").Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, inputs); err != nil {
		return "", err
	}
	// Collapse the whitespace a folded (>-) YAML scalar leaves behind.
	return strings.Join(strings.Fields(b.String()), " "), nil
}
