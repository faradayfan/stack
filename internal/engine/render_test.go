package engine

import "testing"

// TestRender_FoldedCollapsesToOneLine: a folded (>-) step command arrives with
// newlines already turned to spaces by YAML; render collapses the leftover
// wrapping whitespace into single spaces on one line.
func TestRender_FoldedCollapsesToOneLine(t *testing.T) {
	// simulate what YAML hands us for a `>-` scalar: spaces, no newlines.
	tmpl := "helm upgrade --install {{.release}}   {{.chart}}    -n {{.ns}}"
	got, err := render(tmpl, map[string]any{"release": "x", "chart": "./c", "ns": "n"})
	if err != nil {
		t.Fatal(err)
	}
	want := "helm upgrade --install x ./c -n n"
	if got != want {
		t.Errorf("folded render:\n got  %q\n want %q", got, want)
	}
}

// TestRender_LiteralKeepsNewlines: a literal (|) block — e.g. an asdf manager
// install op with three commands — must keep its line breaks. Regression: render
// collapsed newlines to spaces, so `... || true\nasdf install ...` became
// `... || true asdf install ...` and the install silently no-op'd.
func TestRender_LiteralKeepsNewlines(t *testing.T) {
	tmpl := "asdf plugin add {{.plugin}} 2>/dev/null || true\n" +
		"asdf install {{.plugin}} {{.version}}\n" +
		"asdf reshim {{.plugin}}"
	got, err := render(tmpl, map[string]any{"plugin": "golangci-lint", "version": "2.12.2"})
	if err != nil {
		t.Fatal(err)
	}
	want := "asdf plugin add golangci-lint 2>/dev/null || true\n" +
		"asdf install golangci-lint 2.12.2\n" +
		"asdf reshim golangci-lint"
	if got != want {
		t.Errorf("literal render must keep newlines:\n got  %q\n want %q", got, want)
	}
}
