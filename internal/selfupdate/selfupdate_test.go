package selfupdate

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.1.0", "0.1.1", true},
		{"0.1.0", "1.0.0", true},
		{"v0.1.0", "v0.2.0", true}, // leading v tolerated
		{"0.2.0", "0.1.0", false},
		{"0.1.0", "0.1.0", false}, // same → not newer
		{"1.0.0", "0.9.9", false},
		{"0.1.0", "0.1.0-rc1", false}, // prerelease of same base → not newer
	}
	for _, c := range cases {
		got, err := isNewer(c.current, c.latest)
		if err != nil {
			t.Errorf("isNewer(%q,%q): %v", c.current, c.latest, err)
			continue
		}
		if got != c.want {
			t.Errorf("isNewer(%q,%q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

// TestAssetName: the platform asset name strips the leading v and uses os_arch.
func TestAssetName(t *testing.T) {
	if got := assetName("v0.2.0", "darwin", "arm64"); got != "stack_0.2.0_darwin_arm64.tar.gz" {
		t.Errorf("assetName = %q", got)
	}
	if got := assetName("0.2.0", "linux", "amd64"); got != "stack_0.2.0_linux_amd64.tar.gz" {
		t.Errorf("assetName (no v) = %q", got)
	}
}
