package engine

import (
	"reflect"
	"strings"
	"testing"
)

// TestResolveToken_Embedded: tokens resolve embedded anywhere in a string, not
// only when the whole value is a token.
func TestResolveToken_Embedded(t *testing.T) {
	// now_unix is dynamic; assert the surrounding text + that the token is gone.
	got := resolveToken("built-{{ now_unix }}")
	if !strings.HasPrefix(got, "built-") || strings.Contains(got, "{{") {
		t.Errorf("embedded now_unix: got %q", got)
	}
	// a non-token string is untouched (and must NOT shell out to git).
	if got := resolveToken("just a string"); got != "just a string" {
		t.Errorf("passthrough: got %q", got)
	}
	// a value containing a command-template ref (different layer) is left alone.
	if got := resolveToken("{{.release}}"); got != "{{.release}}" {
		t.Errorf("command-template ref must pass through: got %q", got)
	}
}

// TestResolveTokensDeep: resolution recurses maps, lists, and lists-of-maps,
// touching every leaf string.
func TestResolveTokensDeep(t *testing.T) {
	in := map[string]any{
		"scalar": "{{ now_unix }}",
		"list":   []any{"a", "cfg-{{ now_unix }}.yaml"},
		"map":    map[string]any{"image.tag": "{{ now_unix }}"},
		"repos":  []any{map[string]any{"url": "x-{{ now_unix }}"}},
		"plain":  "untouched",
		"num":    42,
	}
	out := resolveTokensDeep(in).(map[string]any)

	if s := out["scalar"].(string); strings.Contains(s, "{{") {
		t.Errorf("scalar not resolved: %q", s)
	}
	if l := out["list"].([]any); l[0] != "a" || strings.Contains(l[1].(string), "{{") {
		t.Errorf("list not resolved: %v", l)
	}
	if m := out["map"].(map[string]any); strings.Contains(m["image.tag"].(string), "{{") {
		t.Errorf("nested map not resolved: %v", m)
	}
	if r := out["repos"].([]any); strings.Contains(r[0].(map[string]any)["url"].(string), "{{") {
		t.Errorf("list-of-maps not resolved: %v", r)
	}
	if out["plain"] != "untouched" {
		t.Errorf("plain string changed: %v", out["plain"])
	}
	if out["num"] != 42 {
		t.Errorf("non-string changed: %v", out["num"])
	}
}

// TestResolveTokensDeep_NoMutation: the input tree is not mutated in place.
func TestResolveTokensDeep_NoMutation(t *testing.T) {
	in := map[string]any{"set": map[string]any{"k": "{{ now_unix }}"}}
	_ = resolveTokensDeep(in)
	inner := in["set"].(map[string]any)
	if inner["k"] != "{{ now_unix }}" {
		t.Errorf("input mutated: %v", inner["k"])
	}
	_ = reflect.DeepEqual // keep import if assertions change
}
