package engine

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/faradayfan/stack/internal/config"
	"github.com/faradayfan/stack/internal/plugins"
)

// SetupResult is the outcome of resolving one tool's setup.
type SetupResult struct {
	Tool      string
	Method    string // "asdf" | "unmanaged" | "manual" | "none"
	Present   bool
	Want      string // pinned/expected version ("" if unknown)
	Have      string // detected version ("" if absent)
	Installed bool   // an install was run
	Verified  bool   // post-install version matched
	Action    string // human note
	Err       error
}

// Setup ensures the tools the checks need are installed at the pinned versions,
// via the configured tools manager (asdf) or each tool's unmanaged fallback.
// doctorOnly (--check) diagnoses without installing. Returns per-tool results and
// whether the overall setup is satisfied.
func (e *Engine) Setup(doctorOnly bool) ([]SetupResult, bool, error) {
	mgrName := e.Cfg.ToolsManager
	tools := toolsFromChecks(e.Cfg.Pattern.SortedChecks())
	if len(tools) == 0 {
		return nil, true, fmt.Errorf("no checks declare tools to set up")
	}

	var mgr plugins.Manager
	var haveMgr bool
	if mgrName != "" {
		mgr, haveMgr = e.Plugins.Manager(mgrName)
		if !haveMgr {
			return nil, false, fmt.Errorf("unknown tools_manager %q (no manager manifest)", mgrName)
		}
	}

	var results []SetupResult
	ok := true
	for _, tool := range tools {
		r := e.setupOne(tool, mgrName, mgr, haveMgr, doctorOnly)
		if r.Err != nil || (!r.Present && !r.Installed) || (r.Installed && !r.Verified) {
			// A tool that's still unsatisfied after setup fails the run. (manual /
			// no-manager cases surface as not-present.)
			if r.Method != "manual" || !doctorOnly {
				ok = ok && r.Present
			}
		}
		results = append(results, r)
	}
	return results, ok, nil
}

// setupOne resolves a single tool.
func (e *Engine) setupOne(tool, mgrName string, mgr plugins.Manager, haveMgr, doctorOnly bool) SetupResult {
	r := SetupResult{Tool: tool}
	m, ok := e.Plugins.Get(tool)
	if !ok {
		r.Err = fmt.Errorf("unknown tool")
		return r
	}
	if m.Setup == nil {
		r.Method = "manual"
		r.Have, r.Present = e.toolVersion(m)
		r.Action = "no setup method — install manually"
		return r
	}

	switch {
	case m.Setup.Asdf != "":
		r.Method = "asdf"
		if mgrName == "" {
			r.Action = "needs a tools_manager (e.g. asdf)"
			r.Have, r.Present = e.toolVersion(m)
			return r
		}
		if !haveMgr {
			r.Err = fmt.Errorf("manager %q not available", mgrName)
			return r
		}
		r.Want = e.pinnedVersion(mgr, m.Setup.Asdf, m.Setup.Dir)
		return e.resolveManaged(m, mgr, r, doctorOnly)

	case m.Setup.Unmanaged != "":
		r.Method = "unmanaged"
		r.Want = m.Setup.Version
		return e.resolveUnmanaged(m, r, doctorOnly)

	default:
		r.Method = "manual"
		r.Action = "setup block declares no method"
		return r
	}
}

// resolveManaged handles an asdf-managed tool: detect, compare to pin, install +
// verify if needed.
func (e *Engine) resolveManaged(m plugins.Manifest, mgr plugins.Manager, r SetupResult, doctorOnly bool) SetupResult {
	r.Have, r.Present = e.toolVersion(m)
	if r.Present && (r.Want == "" || plugins.SameVersion(r.Have, r.Want)) {
		r.Action = "ok"
		return r
	}
	if doctorOnly {
		r.Action = fmt.Sprintf("install: asdf %s %s", m.Setup.Asdf, r.Want)
		return r
	}
	if r.Want == "" {
		r.Err = fmt.Errorf("no pinned version for %s in .tool-versions", m.Setup.Asdf)
		return r
	}
	cmd, err := render(mgr.Ops.Install, map[string]any{"plugin": m.Setup.Asdf, "version": r.Want})
	if err != nil {
		r.Err = err
		return r
	}
	if err := e.run(cmd); err != nil {
		r.Err = err
		return r
	}
	r.Installed = true
	return e.verify(m, r)
}

// resolveUnmanaged handles a go-install (or other literal) tool.
func (e *Engine) resolveUnmanaged(m plugins.Manifest, r SetupResult, doctorOnly bool) SetupResult {
	r.Have, r.Present = e.toolVersion(m)
	if r.Present && (r.Want == "" || plugins.SameVersion(r.Have, r.Want)) {
		r.Action = "ok"
		return r
	}
	if doctorOnly {
		r.Action = "install: " + m.Setup.Unmanaged
		return r
	}
	if err := e.run(m.Setup.Unmanaged); err != nil {
		r.Err = err
		return r
	}
	r.Installed = true
	return e.verify(m, r)
}

// verify re-detects the tool after install and checks the version against want.
func (e *Engine) verify(m plugins.Manifest, r SetupResult) SetupResult {
	have, present := e.toolVersion(m)
	r.Have = have
	if !present {
		r.Err = fmt.Errorf("installed but %s is still not detected", m.Name())
		return r
	}
	if r.Want != "" && !plugins.SameVersion(have, r.Want) {
		r.Err = fmt.Errorf("installed %s but version is %s, expected %s", m.Name(), have, r.Want)
		return r
	}
	r.Verified = true
	r.Action = "installed"
	return r
}

// toolVersion runs a tool's detect (from its setup.dir, if any) and extracts its
// version. Returns (version, present). A detect failure means the tool isn't
// installed (present=false). The dir matters when an asdf-pinned tool only
// resolves inside the subdir that pins it (e.g. node in frontend/).
func (e *Engine) toolVersion(m plugins.Manifest) (string, bool) {
	if m.Detect == "" {
		return "", true // nothing to detect; assume present
	}
	c := exec.Command("sh", "-c", m.Detect)
	if m.Setup != nil && m.Setup.Dir != "" {
		c.Dir = m.Setup.Dir
	}
	out, err := c.CombinedOutput()
	if err != nil {
		return "", false
	}
	v, err := plugins.ExtractVersion(string(out), m.VersionPattern)
	if err != nil {
		// Present, but the version isn't parseable (e.g. a go-installed tool that
		// reports "dev"). Report it as present without a noisy version string.
		return "present", true
	}
	return v, true
}

// pinnedVersion reads the pinned version for a plugin via the manager's `pinned`
// op (empty if not pinned).
func (e *Engine) pinnedVersion(mgr plugins.Manager, plugin, dir string) string {
	if mgr.Ops.Pinned == "" {
		return ""
	}
	cmd, err := render(mgr.Ops.Pinned, map[string]any{"plugin": plugin})
	if err != nil {
		return ""
	}
	c := exec.Command("sh", "-c", cmd)
	if dir != "" {
		c.Dir = dir // the pin lives in the subdir's .tool-versions (e.g. frontend/)
	}
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// oneLine collapses any whitespace (incl. newlines) to single spaces, truncating
// long values — so a verbose/multi-line detect output can't break the summary.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 40 {
		s = s[:37] + "…"
	}
	return s
}

// toolsFromChecks returns the distinct tool names referenced by the checks.
func toolsFromChecks(checks []config.Check) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range checks {
		if c.Tool != "" && !seen[c.Tool] {
			seen[c.Tool] = true
			out = append(out, c.Tool)
		}
	}
	return out
}

// SetupSummary renders a per-tool setup result block.
func SetupSummary(rs []SetupResult) string {
	var b strings.Builder
	b.WriteString("\nsetup:\n")
	for _, r := range rs {
		mark := "ok  "
		switch {
		case r.Err != nil:
			mark = "FAIL"
		case r.Installed && r.Verified:
			mark = "set "
		case !r.Present:
			mark = "miss"
		}
		line := fmt.Sprintf("  %s  %-14s", mark, r.Tool)
		if r.Have != "" {
			line += " have=" + oneLine(r.Have)
		}
		if r.Want != "" {
			line += " want=" + r.Want
		}
		if r.Err != nil {
			line += "  " + r.Err.Error()
		} else if r.Action != "" && r.Action != "ok" {
			line += "  " + r.Action
		}
		fmt.Fprintln(&b, line)
	}
	return b.String()
}
