package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// State is the per-repo selected environment (kubectl-style current-context),
// stored under ~/.stack/config/<repo-key>. Per-user, never committed.
type State struct {
	CurrentEnv string `yaml:"current_env"`
}

// stateDir is ~/.stack/config (overridable via STACK_HOME for tests).
func stateDir() (string, error) {
	if h := os.Getenv("STACK_HOME"); h != "" {
		return filepath.Join(h, "config"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".stack", "config"), nil
}

// repoKey derives a stable, filesystem-safe key for a repo root path, so two
// different checkouts of the same repo (or different repos) don't collide.
func repoKey(repoRoot string) string {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		abs = repoRoot
	}
	sum := sha256.Sum256([]byte(abs))
	base := filepath.Base(abs)
	return base + "-" + hex.EncodeToString(sum[:])[:8]
}

func statePath(repoRoot string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, repoKey(repoRoot)+".yaml"), nil
}

// LoadState reads the selected env for a repo (empty State if none set).
func LoadState(repoRoot string) (State, error) {
	p, err := statePath(repoRoot)
	if err != nil {
		return State{}, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	return s, yaml.Unmarshal(b, &s)
}

// SaveState writes the selected env for a repo.
func SaveState(repoRoot string, s State) error {
	p, err := statePath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// FindRepoRoot walks up from `start` to the nearest directory containing a
// `.stack/` dir (the repo's stack config). Falls back to start if none found.
func FindRepoRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, StackDir)); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir || strings.HasSuffix(parent, string(filepath.Separator)) && parent == string(filepath.Separator) {
			return start
		}
		if parent == dir {
			return start
		}
		dir = parent
	}
}
