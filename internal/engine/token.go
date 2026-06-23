package engine

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Runtime template tokens stack resolves in config values BEFORE a command
// template renders. They are an allowlist (not arbitrary templating), so any
// other `{{...}}` in a config value — e.g. a command-template ref a tool might
// carry — passes through untouched for the command template to handle.
//
// Resolution happens at the stepInputs choke point (engine.stepInputs), so EVERY
// tool's config string gets it uniformly — there is no per-stage special case.
var tokens = map[string]func() string{
	"{{ now_unix }}":      nowUnix,
	"{{now_unix}}":        nowUnix,
	"{{ git_short_sha }}": gitShortSHA,
	"{{git_short_sha}}":   gitShortSHA,
}

func nowUnix() string { return strconv.FormatInt(time.Now().Unix(), 10) }

func gitShortSHA() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// resolveToken replaces every known token wherever it appears in s (embedded or
// whole-value). Strings with no token marker are returned untouched and never
// shell out — git is only run when a git token is actually present.
func resolveToken(s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	for tok, fn := range tokens {
		if strings.Contains(s, tok) {
			s = strings.ReplaceAll(s, tok, fn())
		}
	}
	return s
}

// resolveTokensDeep returns a copy of v with tokens resolved in every leaf string,
// recursing into maps and slices. Non-string leaves and the input tree are left
// unmodified (a fresh structure is returned).
func resolveTokensDeep(v any) any {
	switch t := v.(type) {
	case string:
		return resolveToken(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = resolveTokensDeep(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = resolveTokensDeep(val)
		}
		return out
	default:
		return v
	}
}
