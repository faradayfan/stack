package selfupdate

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

// TestCheckCached_RefreshesAndCaches: first call hits the network and writes the
// cache; a second call within the TTL serves from cache (no network).
func TestCheckCached_RefreshesAndCaches(t *testing.T) {
	t.Setenv("STACK_HOME", t.TempDir())

	var hits int
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/repos/faradayfan/stack/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Write([]byte(`{"tag_name":"v0.2.0"}`))
	})
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	// First call: stale (no cache) → refreshes.
	tag, newer := CheckCached("0.1.0", 24*time.Hour)
	if tag != "v0.2.0" || !newer {
		t.Fatalf("first check: tag=%q newer=%v", tag, newer)
	}
	if hits != 1 {
		t.Fatalf("expected 1 network hit, got %d", hits)
	}

	// Second call within TTL: served from cache, no new network hit.
	tag, newer = CheckCached("0.1.0", 24*time.Hour)
	if tag != "v0.2.0" || !newer {
		t.Fatalf("cached check: tag=%q newer=%v", tag, newer)
	}
	if hits != 1 {
		t.Errorf("cache should avoid a 2nd network hit, got %d", hits)
	}
}

// TestCheckCached_SilentOnNetworkError: a failing endpoint yields no tag and no
// panic — the check is best-effort.
func TestCheckCached_SilentOnNetworkError(t *testing.T) {
	t.Setenv("STACK_HOME", t.TempDir())
	old := apiBase
	apiBase = "http://127.0.0.1:0" // unreachable
	defer func() { apiBase = old }()

	tag, newer := CheckCached("0.1.0", 24*time.Hour)
	if tag != "" || newer {
		t.Errorf("expected silent (empty) result on error, got tag=%q newer=%v", tag, newer)
	}
}

// TestCheckCached_NotNewer: when the cached latest equals current, newer is false.
func TestCheckCached_NotNewer(t *testing.T) {
	t.Setenv("STACK_HOME", t.TempDir())
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/repos/faradayfan/stack/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.1.0"}`))
	})
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	tag, newer := CheckCached("0.1.0", 24*time.Hour)
	if newer {
		t.Errorf("v0.1.0 vs current 0.1.0 should not be newer (tag=%q)", tag)
	}
	_ = runtime.GOOS
}
