package engine

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/faradayfan/stack/internal/config"
)

// CheckResult is the outcome of one check.
type CheckResult struct {
	Name     string
	Command  string
	Blocking bool
	Passed   bool
	Skipped  bool
	Err      error
}

// Check runs the `stack check` flow: every check in the app's list (optionally
// filtered to `only`), in parallel where independent, honoring blocking-ness.
// Returns the per-check results and whether the overall run passed (all blocking
// checks passed). A non-blocking failure does not fail the run.
//
// Checks with an `after:` dependency (e.g. an image scan that needs build-artifact)
// are SKIPPED in standalone `stack check` — they belong to a combined deploy+check
// flow where the artifact exists. They run there, not here.
func (e *Engine) Check(only []string) ([]CheckResult, bool, error) {
	checks := selectChecks(e.Cfg.App.SortedChecks(), only)
	if len(checks) == 0 {
		return nil, true, fmt.Errorf("no checks to run (declare `checks:` in .stack/app.yaml)")
	}

	// Resolve each check to a command. A resolve failure (unknown tool, tool
	// binary not on PATH for version detection, render error) is recorded as a
	// FAILED result for THAT check — it must not abort the other checks. A
	// blocking check that can't resolve fails the run; a non-blocking one warns.
	type job struct {
		c   config.Check
		cmd string
	}
	var jobs []job
	results := make([]CheckResult, 0, len(checks))
	for _, c := range checks {
		if c.After != "" {
			results = append(results, CheckResult{Name: c.Name, Blocking: c.IsBlocking(), Skipped: true})
			continue
		}
		cmd, err := e.renderCheck(c)
		if err != nil {
			results = append(results, CheckResult{
				Name: c.Name, Blocking: c.IsBlocking(), Passed: false, Err: err,
			})
			_, _ = fmt.Fprintf(e.Out, "[%s] cannot run: %v\n", c.Name, err)
			continue
		}
		jobs = append(jobs, job{c: c, cmd: cmd})
	}

	// Partition into parallel-safe and serial jobs.
	var parallel, serial []job
	for _, j := range jobs {
		if j.c.Serial {
			serial = append(serial, j)
		} else {
			parallel = append(parallel, j)
		}
	}

	// run executes a job, capturing its output into a private buffer (so parallel
	// checks never write to the shared e.Out concurrently — that's a data race).
	// The captured output is flushed to e.Out by the caller, in order.
	run := func(j job) (CheckResult, string) {
		r := CheckResult{Name: j.c.Name, Command: j.cmd, Blocking: j.c.IsBlocking()}
		var local bytes.Buffer
		if e.DryRun {
			fmt.Fprintf(&local, "[%s] %s\n", j.c.Name, j.cmd)
			r.Passed = true
			return r, local.String()
		}
		r.Err = runCheckInto(&local, j.cmd, j.c.Dir)
		r.Passed = r.Err == nil
		return r, local.String()
	}

	// Parallel jobs, bounded pool; each writes to its own buffer.
	if len(parallel) > 0 {
		sem := make(chan struct{}, maxParallel())
		rs := make([]CheckResult, len(parallel))
		outs := make([]string, len(parallel))
		var wg sync.WaitGroup
		for i, j := range parallel {
			wg.Add(1)
			go func(i int, j job) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				rs[i], outs[i] = run(j)
			}(i, j)
		}
		wg.Wait()
		for i := range parallel {
			_, _ = fmt.Fprint(e.Out, outs[i])
			results = append(results, rs[i])
		}
	}
	// Serial jobs, one at a time.
	for _, j := range serial {
		r, out := run(j)
		_, _ = fmt.Fprint(e.Out, out)
		results = append(results, r)
	}

	return results, overallPassed(results), nil
}

// renderCheck resolves a check's tool manifest and renders its `check` command
// with the entry's args as template inputs.
func (e *Engine) renderCheck(c config.Check) (string, error) {
	m, ok := e.Plugins.Get(c.Tool)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", c.Tool)
	}
	if !m.ProvidesStep("check") {
		return "", fmt.Errorf("tool %q does not provide the `check` step", c.Tool)
	}
	v, err := e.detectIn(m, c.Dir) // honor the check's working dir for version detection
	if err != nil {
		return "", err
	}
	tmpl, ok, err := m.CommandFor("check", v)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("tool %q has no check command for v%s", c.Tool, v)
	}
	return render(tmpl, c.Args)
}

// runCheckInto executes a check command (optionally from `dir`), writing the
// command echo and the tool's stdout/stderr into w (a per-check buffer).
func runCheckInto(w io.Writer, cmd, dir string) error {
	_, _ = fmt.Fprintf(w, "+ %s%s\n", dirPrefix(dir), cmd)
	c := exec.Command("sh", "-c", cmd)
	if dir != "" {
		c.Dir = dir
	}
	c.Stdout, c.Stderr = w, w
	return c.Run()
}

func dirPrefix(dir string) string {
	if dir == "" {
		return ""
	}
	return "(" + dir + ") "
}

// selectChecks filters the list to the named ones (all when `only` is empty).
func selectChecks(all []config.Check, only []string) []config.Check {
	if len(only) == 0 {
		return all
	}
	want := map[string]bool{}
	for _, n := range only {
		want[n] = true
	}
	var out []config.Check
	for _, c := range all {
		if want[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// overallPassed: the run passes iff every BLOCKING, non-skipped check passed.
func overallPassed(rs []CheckResult) bool {
	for _, r := range rs {
		if r.Blocking && !r.Skipped && !r.Passed {
			return false
		}
	}
	return true
}

// Summary renders a human-readable per-check result block.
func Summary(rs []CheckResult) string {
	var b strings.Builder
	b.WriteString("\nchecks:\n")
	for _, r := range rs {
		mark := "ok  "
		switch {
		case r.Skipped:
			mark = "skip"
		case !r.Passed && r.Blocking:
			mark = "FAIL"
		case !r.Passed && !r.Blocking:
			mark = "warn" // non-blocking failure
		}
		fmt.Fprintf(&b, "  %s  %s\n", mark, r.Name)
	}
	return b.String()
}

func maxParallel() int {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	if n > 8 {
		return 8 // cap; integration tests are heavy
	}
	return n
}
