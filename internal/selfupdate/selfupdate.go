// Package selfupdate checks GitHub Releases for a newer stack binary and, on
// request, downloads it, verifies its checksum, and atomically replaces the
// running executable.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// repo is the GitHub owner/repo stack updates from.
const repo = "faradayfan/stack"

// Release is the subset of the GitHub release API we use.
type Release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// apiBase is overridable in tests; defaults to GitHub.
var apiBase = "https://api.github.com"

var httpClient = &http.Client{Timeout: 30 * time.Second}

// Latest fetches the latest published release.
func Latest() (Release, error) {
	var r Release
	url := fmt.Sprintf("%s/repos/%s/releases/latest", apiBase, repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return r, fmt.Errorf("query latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return r, fmt.Errorf("query latest release: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return r, fmt.Errorf("decode release: %w", err)
	}
	if r.Tag == "" {
		return r, fmt.Errorf("latest release has no tag")
	}
	return r, nil
}

// --- version comparison (pure) -------------------------------------------------

type semver struct{ major, minor, patch int }

func parse(s string) (semver, bool, error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "v"))
	pre := false
	if i := strings.IndexAny(s, "-+"); i >= 0 { // -prerelease / +build
		pre = true
		s = s[:i]
	}
	parts := strings.SplitN(s, ".", 3)
	var v semver
	dst := []*int{&v.major, &v.minor, &v.patch}
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return v, pre, fmt.Errorf("invalid version %q", s)
		}
		*dst[i] = n
	}
	return v, pre, nil
}

// isNewer reports whether latest is a strictly newer release than current. A
// prerelease of the same base version is NOT newer (it's a pre-release).
func isNewer(current, latest string) (bool, error) {
	c, _, err := parse(current)
	if err != nil {
		return false, err
	}
	l, _, err := parse(latest)
	if err != nil {
		return false, err
	}
	switch {
	case l.major != c.major:
		return l.major > c.major, nil
	case l.minor != c.minor:
		return l.minor > c.minor, nil
	case l.patch != c.patch:
		return l.patch > c.patch, nil
	default:
		// same base version (incl. a prerelease of it) → not an upgrade.
		return false, nil
	}
}

// assetName is the release asset for a tag + platform. GoReleaser names assets
// stack_<version-without-v>_<os>_<arch>.tar.gz.
func assetName(tag, goos, goarch string) string {
	return fmt.Sprintf("stack_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), goos, goarch)
}

// --- check + update ------------------------------------------------------------

// Check returns the latest release and whether it is newer than current.
func Check(current string) (rel Release, newer bool, err error) {
	rel, err = Latest()
	if err != nil {
		return rel, false, err
	}
	newer, err = isNewer(current, rel.Tag)
	return rel, newer, err
}

// Update downloads the latest release for this platform, verifies its checksum,
// and atomically replaces the running binary. It returns the version installed.
// current must be a real version (not "dev"); the caller guards that.
func Update(current string) (string, error) {
	rel, newer, err := Check(current)
	if err != nil {
		return "", err
	}
	if !newer {
		return rel.Tag, nil // up to date; caller reports
	}

	want := assetName(rel.Tag, runtime.GOOS, runtime.GOARCH)
	assetURL, sumsURL := "", ""
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			assetURL = a.URL
		case "checksums.txt":
			sumsURL = a.URL
		}
	}
	if assetURL == "" {
		return "", fmt.Errorf("no release asset %q for %s/%s", want, runtime.GOOS, runtime.GOARCH)
	}

	// Where are we replacing? Resolve before downloading so we fail fast on a
	// non-writable location rather than after pulling the binary.
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current binary: %w", err)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	if err := writable(filepath.Dir(exe)); err != nil {
		return "", fmt.Errorf("cannot replace %s: %w", exe, err)
	}

	tarGz, err := download(assetURL)
	if err != nil {
		return "", err
	}

	if sumsURL != "" {
		sums, err := downloadBytes(sumsURL)
		if err != nil {
			return "", err
		}
		if err := verifyChecksum(tarGz, want, sums); err != nil {
			return "", err
		}
	}

	newBin, err := extractBinary(tarGz)
	if err != nil {
		return "", err
	}

	if err := replace(exe, newBin); err != nil {
		return "", err
	}
	return rel.Tag, nil
}

// --- download / verify / extract / replace ------------------------------------

func download(url string) ([]byte, error) { return downloadBytes(url) }

func downloadBytes(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// verifyChecksum confirms data's sha256 matches the line for `name` in a
// `sha256  name` checksums file. A mismatch (or missing entry) is a hard error.
func verifyChecksum(data []byte, name string, sums []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == name {
			if f[0] == got {
				return nil
			}
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, f[0])
		}
	}
	return fmt.Errorf("no checksum entry for %s", name)
}

// extractBinary pulls the `stack` executable out of a .tar.gz into a temp file
// next to nothing (returned path); the caller renames it into place.
func extractBinary(tarGz []byte) (string, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(tarGz)))
	if err != nil {
		return "", fmt.Errorf("gunzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("no `stack` binary in archive")
		}
		if err != nil {
			return "", fmt.Errorf("read archive: %w", err)
		}
		if filepath.Base(hdr.Name) != "stack" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		tmp, err := os.CreateTemp("", "stack-update-*")
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(tmp, tr); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return "", err
		}
		_ = tmp.Close()
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			_ = os.Remove(tmp.Name())
			return "", err
		}
		return tmp.Name(), nil
	}
}

// replace atomically swaps newBin in for the running binary at exe. It renames
// when possible (same filesystem) and falls back to a copy across filesystems.
func replace(exe, newBin string) error {
	// Try an atomic rename first (works when temp + exe share a filesystem).
	if err := os.Rename(newBin, exe); err == nil {
		return nil
	}
	// Cross-filesystem: stage a temp next to exe, then rename over.
	staged := exe + ".new"
	if err := copyFile(newBin, staged); err != nil {
		return err
	}
	_ = os.Remove(newBin)
	if err := os.Chmod(staged, 0o755); err != nil {
		_ = os.Remove(staged)
		return err
	}
	if err := os.Rename(staged, exe); err != nil {
		_ = os.Remove(staged)
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// writable checks that dir is writable by creating + removing a temp file.
func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".stack-write-test-*")
	if err != nil {
		return fmt.Errorf("directory not writable (try a different install location, or run with the right permissions)")
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}
