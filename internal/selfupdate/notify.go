package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// cache is the on-disk record of the last update check.
type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	LatestTag string    `json:"latest_tag"`
}

// cacheFile is ~/.stack/config/update-check.json (STACK_HOME overrides the base,
// matching the rest of stack's per-user state).
func cacheFile() (string, error) {
	base := os.Getenv("STACK_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".stack")
	}
	dir := filepath.Join(base, "config")
	return filepath.Join(dir, "update-check.json"), nil
}

func readCache() (cache, bool) {
	path, err := cacheFile()
	if err != nil {
		return cache{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cache{}, false
	}
	var c cache
	if err := json.Unmarshal(b, &c); err != nil {
		return cache{}, false
	}
	return c, true
}

func writeCache(c cache) {
	path, err := cacheFile()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if b, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}

// now is overridable in tests (kept simple; defaults to time.Now).
var now = time.Now

// CheckCached returns the latest release tag and whether it is newer than
// current, using a daily-refreshed cache. It hits the network only when the
// cache is missing or older than ttl; otherwise it answers instantly from cache.
// It is best-effort: any error (network, parse) yields ("", false) silently, so a
// caller can wire it into every command without risk of slowness or failure.
func CheckCached(current string, ttl time.Duration) (latestTag string, newer bool) {
	c, ok := readCache()
	if !ok || now().Sub(c.CheckedAt) > ttl {
		rel, err := Latest()
		if err != nil {
			return "", false // silent
		}
		c = cache{CheckedAt: now(), LatestTag: rel.Tag}
		writeCache(c)
	}
	if c.LatestTag == "" {
		return "", false
	}
	n, err := isNewer(current, c.LatestTag)
	if err != nil {
		return "", false
	}
	return c.LatestTag, n
}
