package engine

// BuildNative is the native pattern's `build` stage: for each artifact in
// `artifacts:`, run the bound build tool's build-artifact step. A Go binary
// build renders `go build -o <output> <package>` per artifact. The k8s build
// stage is k8sBuild (in pattern_k8s.go); both are invoked by the pipeline runner.
func (e *Engine) BuildNative() error {
	for _, a := range e.Cfg.Pattern.SortedArtifacts() {
		if _, err := e.Step("build-artifact", map[string]any{
			"package": a.Package,
			"output":  a.Output,
			"ldflags": a.Ldflags,
		}); err != nil {
			return err
		}
	}
	return nil
}
