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

// Engine carries the resolved config, the plugin registry, and run options.
type Engine struct {
	Cfg      config.Merged
	Plugins  *plugins.Registry
	DryRun   bool
	Out      io.Writer         // where dry-run lines / progress go (default os.Stdout)
	versions map[string]string // tool name → detected version (cache)
}

// New builds an engine for a resolved config.
func New(cfg config.Merged, reg *plugins.Registry, dryRun bool) *Engine {
	return &Engine{Cfg: cfg, Plugins: reg, DryRun: dryRun, Out: os.Stdout, versions: map[string]string{}}
}

// NewForChecks builds an engine for the env-independent check flow, needing only
// the app config (no selected environment).
func NewForChecks(app config.App, reg *plugins.Registry, dryRun bool) *Engine {
	return New(config.Merged{App: app}, reg, dryRun)
}

// Step resolves the abstract step to its bound tool, renders the command with
// `inputs`, and runs it. The tool is taken from the env's `tools` binding, or a
// pattern default (defaultTool) when the context omits it — so a minimal context
// still tears down/observes. A step with neither is an error (mis-wired context).
// Returns the rendered command (useful for tests / fixtures) even on dry-run.
//
// The template inputs are composed (lowest→highest precedence): env IDENTITY
// (kube_context, namespace, delivery) < the step's tool CONFIG (from its binding)
// < the caller-supplied dynamic `inputs` (e.g. per-image `ref`). So tool-specific
// settings live in the binding's config, shared identity comes from the env root,
// and the pattern only passes the truly dynamic bits.
func (e *Engine) Step(step string, inputs map[string]any) (string, error) {
	binding, ok := e.Cfg.Env.Tools[step]
	if !ok {
		if t, has := defaultTool(e.Cfg.Env.Pattern, step); has {
			binding = config.ToolBinding{Tool: t}
		} else {
			return "", fmt.Errorf("no tool bound for step %q in env %q (and no pattern default)", step, e.Cfg.EnvName)
		}
	}
	m, ok := e.Plugins.Get(binding.Tool)
	if !ok {
		return "", fmt.Errorf("step %q bound to unknown tool %q", step, binding.Tool)
	}
	version, err := e.detect(m)
	if err != nil {
		return "", err
	}
	tmpl, ok, err := m.CommandFor(step, version)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool %q (v%s) does not provide step %q", binding.Tool, version, step)
	}
	cmd, err := render(tmpl, e.stepInputs(binding, inputs))
	if err != nil {
		return "", fmt.Errorf("render %q for step %q: %w", binding.Tool, step, err)
	}
	if err := e.run(cmd); err != nil {
		return cmd, err
	}
	return cmd, nil
}

// ValidateBindings checks every tool binding's config against its manifest's
// declared config schema (required keys present, no unknown keys). Run before a
// flow so a typo'd config key fails up front with a clear message, not at
// command-render time.
func (e *Engine) ValidateBindings() error {
	for step, b := range e.Cfg.Env.Tools {
		m, ok := e.Plugins.Get(b.Tool)
		if !ok {
			return fmt.Errorf("step %q: unknown tool %q", step, b.Tool)
		}
		if err := m.ValidateConfig(b.Config); err != nil {
			return fmt.Errorf("step %q: %w", step, err)
		}
	}
	return nil
}

// stepInputs composes env identity + the step's tool config + the dynamic inputs.
func (e *Engine) stepInputs(b config.ToolBinding, dynamic map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range e.Cfg.Env.Identity() {
		out[k] = v
	}
	for k, v := range b.Config {
		out[k] = v
	}
	for k, v := range dynamic {
		out[k] = v
	}
	return out
}

// binding returns the resolved binding for a step (with pattern default).
func (e *Engine) binding(step string) (config.ToolBinding, bool) {
	if b, ok := e.Cfg.Env.Tools[step]; ok {
		return b, true
	}
	if t, ok := defaultTool(e.Cfg.Env.Pattern, step); ok {
		return config.ToolBinding{Tool: t}, true
	}
	return config.ToolBinding{}, false
}

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
