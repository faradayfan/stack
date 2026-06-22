package plugins

import "testing"

func TestMatchRange(t *testing.T) {
	cases := []struct {
		version, constraints string
		want                 bool
	}{
		{"27.1.1", ">=24.0", true},
		{"23.0.5", ">=24.0", false},
		{"24.0.0", ">=24.0", true}, // boundary inclusive
		{"22.0.0", ">=20.0 <24.0", true},
		{"24.0.0", ">=20.0 <24.0", false}, // upper exclusive
		{"19.9.0", ">=20.0 <24.0", false},
		{"18.0.0", "<20.0", true},
		{"1.26", ">=1.24", true}, // 2-part version
		{"1.23.9", ">=1.24", false},
		{"v0.114.0", ">=0.100", true}, // leading v + 3-part
		{"3.16.4", "=3.16.4", true},
		{"3.16.5", "=3.16.4", false},
		{"24.0.7-rc1", ">=24.0", true}, // prerelease suffix ignored
	}
	for _, c := range cases {
		got, err := matchRange(c.version, c.constraints)
		if err != nil {
			t.Errorf("matchRange(%q,%q) error: %v", c.version, c.constraints, err)
			continue
		}
		if got != c.want {
			t.Errorf("matchRange(%q,%q) = %v, want %v", c.version, c.constraints, got, c.want)
		}
	}
}

func TestParseVer(t *testing.T) {
	if _, err := parseVer("not.a.version"); err == nil {
		t.Error("expected error for non-numeric version")
	}
	v, err := parseVer("v27.1.1")
	if err != nil || v.major != 27 || v.minor != 1 || v.patch != 1 {
		t.Errorf("parseVer(v27.1.1) = %+v, %v", v, err)
	}
}
