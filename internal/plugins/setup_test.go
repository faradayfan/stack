package plugins

import "testing"

func TestExtractVersion(t *testing.T) {
	cases := []struct {
		name, out, pattern, want string
		wantErr                  bool
	}{
		{
			name:    "golangci verbose, with pattern",
			out:     "golangci-lint has version 2.12.2 built with go1.26.4 from c0d3ddc9",
			pattern: `version (\d+\.\d+\.\d+)`,
			want:    "2.12.2",
		},
		{
			name: "node, no pattern → first semver token (strips v)",
			out:  "v26.3.0",
			want: "26.3.0",
		},
		{
			name: "gosec, no pattern, version on its own line",
			out:  "Version: 2.22.5\nGit tag: v2.22.5\nBuild date: 2026-01-01",
			want: "2.22.5",
		},
		{
			name:    "pattern matches nothing → error",
			out:     "no version here",
			pattern: `version (\d+\.\d+\.\d+)`,
			wantErr: true,
		},
		{
			name:    "no semver anywhere → error",
			out:     "command not found",
			wantErr: true,
		},
		{
			name: "two-part version (1.26) accepted by fallback",
			out:  "go version go1.26 darwin/arm64",
			want: "1.26",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ExtractVersion(c.out, c.pattern)
			if c.wantErr {
				if err == nil {
					t.Errorf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSameVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.12.2", "2.12.2", true},
		{"2.12", "2.12.0", true}, // missing patch == 0
		{"v26.3.0", "26.3.0", true},
		{"2.12.2", "2.12.1", false},
		{"2.12.2", "2.13.0", false},
	}
	for _, c := range cases {
		if got := SameVersion(c.a, c.b); got != c.want {
			t.Errorf("SameVersion(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
