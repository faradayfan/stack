// Package config loads and merges the two stack context-file layers:
// .stack/app.yaml (app-wide, environment-independent) and .stack/<env>.yaml
// (one per environment). The merged result drives the engine.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Image is one buildable artifact.
type Image struct {
	Name    string            `yaml:"name"`
	Context string            `yaml:"context"`
	Tag     string            `yaml:"tag,omitempty"`  // defaults to App.DefaultTag
	Args    map[string]string `yaml:"args,omitempty"` // --build-arg
}

// Scan declares which first-party images to vuln-gate and at what threshold.
type Scan struct {
	Images []string `yaml:"images"`
	FailOn string   `yaml:"fail_on,omitempty"` // grype severity; default "high"
}

// HelmRepo is a chart repo to register before `helm dependency build`.
type HelmRepo struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Deps are prerequisites resolved before render/apply.
type Deps struct {
	HelmRepos []HelmRepo `yaml:"helm_repos,omitempty"`
}

// App is .stack/app.yaml — app-wide, environment-independent.
type App struct {
	Name       string  `yaml:"name"`
	DefaultTag string  `yaml:"default_tag,omitempty"` // tag when an Image omits one; default "dev"
	Images     []Image `yaml:"images"`
	Scan       Scan    `yaml:"scan"`
	// Hooks: name → command (M1 keeps the simple string form; the typed
	// env-mapping form lands with the native/seed milestone).
	Hooks map[string]string `yaml:"hooks,omitempty"`
}

// Env is .stack/<env>.yaml — one per environment.
type Env struct {
	Pattern       string            `yaml:"pattern"` // k8s | native | compose
	KubeContext   string            `yaml:"kube_context,omitempty"`
	Namespace     string            `yaml:"namespace,omitempty"`
	Node          string            `yaml:"node,omitempty"`           // for `ctr import` (load mode)
	ImageDelivery string            `yaml:"image_delivery,omitempty"` // load | push
	Registry      string            `yaml:"registry,omitempty"`
	Platform      string            `yaml:"platform,omitempty"`
	Tools         map[string]string `yaml:"tools"` // abstract-step → tool name
	Chart         string            `yaml:"chart,omitempty"`
	Values        []string          `yaml:"values,omitempty"`
	HelmSet       map[string]string `yaml:"helm_set,omitempty"`
	Deps          Deps              `yaml:"deps,omitempty"`
	Remote        bool              `yaml:"remote,omitempty"` // → confirm before deploy/down
	ReleaseName   string            `yaml:"release_name,omitempty"`
}

// Merged is the resolved app + env, ready for the engine.
type Merged struct {
	EnvName string
	App     App
	Env     Env
}

// ReleaseName is the helm release name: explicit release_name, else the app name.
func (m Merged) ReleaseName() string {
	if m.Env.ReleaseName != "" {
		return m.Env.ReleaseName
	}
	return m.App.Name
}

// ImageRef returns name:tag for an image (tag defaults to App.DefaultTag, then "dev").
func (m Merged) ImageRef(img Image) string {
	tag := img.Tag
	if tag == "" {
		tag = m.App.DefaultTag
	}
	if tag == "" {
		tag = "dev"
	}
	return img.Name + ":" + tag
}

// StackDir is the per-repo context directory.
const StackDir = ".stack"

// Load reads .stack/app.yaml and .stack/<env>.yaml from repoRoot and merges them.
func Load(repoRoot, envName string) (Merged, error) {
	var m Merged
	m.EnvName = envName

	appPath := filepath.Join(repoRoot, StackDir, "app.yaml")
	if err := readYAML(appPath, &m.App); err != nil {
		return m, fmt.Errorf("load app config: %w", err)
	}
	envPath := filepath.Join(repoRoot, StackDir, envName+".yaml")
	if err := readYAML(envPath, &m.Env); err != nil {
		return m, fmt.Errorf("load env %q: %w", envName, err)
	}
	if err := m.validate(); err != nil {
		return m, err
	}
	return m, nil
}

func (m Merged) validate() error {
	if m.App.Name == "" {
		return fmt.Errorf("app.yaml: name is required")
	}
	if m.Env.Pattern == "" {
		return fmt.Errorf("%s.yaml: pattern is required", m.EnvName)
	}
	if len(m.Env.Tools) == 0 {
		return fmt.Errorf("%s.yaml: tools mapping is required", m.EnvName)
	}
	return nil
}

func readYAML(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, dst)
}

// ListEnvs returns the available environment names (the <env>.yaml files in
// .stack/, excluding app.yaml).
func ListEnvs(repoRoot string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(repoRoot, StackDir))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || filepath.Ext(n) != ".yaml" || n == "app.yaml" {
			continue
		}
		out = append(out, n[:len(n)-len(".yaml")])
	}
	return out, nil
}
