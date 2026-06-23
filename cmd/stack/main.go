// Command stack stands up, tears down, and deploys an app into a target
// environment, driven by .stack/ context files. See the repo README + docs.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/faradayfan/stack/internal/config"
	"github.com/faradayfan/stack/internal/engine"
	"github.com/faradayfan/stack/internal/plugins"
)

// version is stamped at release build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "stack: "+err.Error())
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var envFlag string
	var patternFlag string
	var dryRun bool

	root := &cobra.Command{
		Use:           "stack",
		Short:         "Run and deploy an app from .stack/ context files",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&envFlag, "env", "", "environment to act on (an .stack/<env>.yaml file)")
	root.PersistentFlags().StringVar(&patternFlag, "pattern", "", "pattern to run directly, with no env overrides")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "print the commands that would run, without running them")

	// resolveEnv: --env flag wins; else the repo's current-context (may be empty).
	resolveEnv := func() (root string, env string) {
		cwd, _ := os.Getwd()
		repo := config.FindRepoRoot(cwd)
		if envFlag != "" {
			return repo, envFlag
		}
		if st, err := config.LoadState(repo); err == nil {
			return repo, st.CurrentEnv
		}
		return repo, ""
	}

	// newEngine resolves the pattern to run, in precedence order:
	//   1. --pattern  → that pattern from app.yaml, no env overrides
	//   2. --env / current-context → that env file (selects a pattern + overrides)
	//   3. neither set → auto-select the sole pattern (error if the app has several)
	// An env file is therefore optional — only needed for per-environment overrides.
	newEngine := func() (*engine.Engine, error) {
		repo, env := resolveEnv()
		if patternFlag != "" && envFlag != "" {
			return nil, fmt.Errorf("--pattern and --env are mutually exclusive")
		}
		var cfg config.Resolved
		var err error
		switch {
		case patternFlag != "":
			cfg, err = config.LoadPattern(repo, patternFlag)
		case env != "":
			cfg, err = config.Load(repo, env)
		default:
			cfg, err = config.LoadPattern(repo, "") // no env → run the sole pattern
		}
		if err != nil {
			return nil, err
		}
		reg, err := plugins.Load()
		if err != nil {
			return nil, err
		}
		return engine.New(cfg, reg, dryRun), nil
	}

	root.AddCommand(
		useCmd(),
		envCmd(newEngine),
		buildCmd(newEngine),
		deployCmd(newEngine),
		downCmd(newEngine),
		statusCmd(newEngine),
		checkCmd(&dryRun, &patternFlag),
		setupCmd(&dryRun, &patternFlag),
	)
	return root
}

// setupCmd installs/verifies the tools the checks need, via the configured tools
// manager (asdf) or each tool's unmanaged fallback.
func setupCmd(dryRun *bool, patternFlag *string) *cobra.Command {
	var doctor bool
	c := &cobra.Command{
		Use:   "setup",
		Short: "Install/verify the tools the checks need (via the tools manager)",
		Long: "Ensure each tool the checks reference is installed at the version the\n" +
			"repo pins. asdf-managed tools install from .tool-versions; tools with no\n" +
			"asdf plugin use their declared unmanaged install. --check diagnoses only.",
		RunE: func(_ *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			repo := config.FindRepoRoot(cwd)
			app, err := config.LoadApp(repo)
			if err != nil {
				return err
			}
			patName, pat, err := app.SelectPattern(*patternFlag)
			if err != nil {
				return err
			}
			reg, err := plugins.Load()
			if err != nil {
				return err
			}
			e := engine.NewForPattern(app, patName, pat, reg, *dryRun)
			results, ok, err := e.Setup(doctor)
			if err != nil {
				return err
			}
			fmt.Print(engine.SetupSummary(results))
			if !ok {
				if doctor {
					return fmt.Errorf("some tools are missing — run `stack setup` to install")
				}
				return fmt.Errorf("setup did not satisfy all tools")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&doctor, "check", false, "diagnose only — report what's missing, install nothing")
	return c
}

// checkCmd runs the env-independent verification flow (the `stack check` /CI flow).
func checkCmd(dryRun *bool, patternFlag *string) *cobra.Command {
	c := &cobra.Command{
		Use:   "check [name...]",
		Short: "Run the verification checks (tests, lint, format, scans) — the CI flow",
		Long: "Run the checks declared under a pattern in .stack/app.yaml. With no args,\n" +
			"runs all; otherwise only the named checks. The pattern is auto-selected when\n" +
			"the app has one; pass --pattern when it has several. Independent checks run\n" +
			"in parallel; non-blocking checks report but never fail the run.",
		RunE: func(_ *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			repo := config.FindRepoRoot(cwd)
			app, err := config.LoadApp(repo)
			if err != nil {
				return err
			}
			patName, pat, err := app.SelectPattern(*patternFlag)
			if err != nil {
				return err
			}
			reg, err := plugins.Load()
			if err != nil {
				return err
			}
			e := engine.NewForPattern(app, patName, pat, reg, *dryRun)
			results, passed, err := e.Check(args)
			if err != nil {
				return err
			}
			fmt.Print(engine.Summary(results))
			if !passed {
				return fmt.Errorf("one or more blocking checks failed")
			}
			return nil
		},
	}
	return c
}

func useCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <env>",
		Short: "Select the current environment for this repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			repo := config.FindRepoRoot(cwd)
			// Validate the env exists before selecting it.
			if _, err := config.Load(repo, args[0]); err != nil {
				return err
			}
			if err := config.SaveState(repo, config.State{CurrentEnv: args[0]}); err != nil {
				return err
			}
			fmt.Printf("now using environment %q\n", args[0])
			return nil
		},
	}
}

func envCmd(newEngine func() (*engine.Engine, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "env",
		Short: "Show the resolved pattern (env, --pattern, or the sole pattern)",
		RunE: func(_ *cobra.Command, _ []string) error {
			e, err := newEngine()
			if err != nil {
				return err
			}
			cfg := e.Cfg
			p := cfg.Pattern
			env := cfg.EnvName
			if env == "" {
				env = "(none)"
			}
			fmt.Printf("environment : %s\n", env)
			fmt.Printf("app         : %s\n", cfg.App)
			fmt.Printf("pattern     : %s\n", cfg.Name)
			fmt.Printf("pipeline    : %v\n", p.Pipeline)
			fmt.Printf("kube_context: %s\n", p.KubeContext)
			fmt.Printf("namespace   : %s\n", p.Namespace)
			fmt.Printf("delivery    : %s\n", p.ImageDelivery)
			if p.Registry != "" {
				fmt.Printf("registry    : %s\n", p.Registry)
			}
			fmt.Println("step → tool :")
			for _, key := range []string{"build", "deliver", "scan", "render", "apply", "wait_ready", "status"} {
				if b, ok := p.Step(key); ok && b.Tool != "" {
					fmt.Printf("  %-12s %s\n", key, b.Tool)
				}
			}
			return nil
		},
	}
}

// buildCmd runs only the build-artifact step for the selected pattern: for a k8s
// pattern it builds every image; for a native pattern it builds every binary
// (e.g. `go build -o bin/stack ./cmd/stack`). No deliver/scan/apply.
func buildCmd(newEngine func() (*engine.Engine, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "Build the app's artifacts (images or binaries) — no deliver/apply",
		RunE: func(_ *cobra.Command, _ []string) error {
			e, err := newEngine()
			if err != nil {
				return err
			}
			return dispatch(e, "build", false)
		},
	}
}

func deployCmd(newEngine func() (*engine.Engine, error)) *cobra.Command {
	c := &cobra.Command{
		Use:     "deploy",
		Aliases: []string{"up"},
		Short:   "Build, scan, deliver, and apply the app into the environment",
		RunE: func(_ *cobra.Command, _ []string) error {
			e, err := newEngine()
			if err != nil {
				return err
			}
			return dispatch(e, "deploy", false)
		},
	}
	return c
}

func downCmd(newEngine func() (*engine.Engine, error)) *cobra.Command {
	var destroy bool
	c := &cobra.Command{
		Use:   "down",
		Short: "Tear down the app (--destroy also drops volumes)",
		RunE: func(_ *cobra.Command, _ []string) error {
			e, err := newEngine()
			if err != nil {
				return err
			}
			return dispatch(e, "down", destroy)
		},
	}
	c.Flags().BoolVar(&destroy, "destroy", false, "also delete persistent volumes")
	return c
}

func statusCmd(newEngine func() (*engine.Engine, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the app's pods in the environment",
		RunE: func(_ *cobra.Command, _ []string) error {
			e, err := newEngine()
			if err != nil {
				return err
			}
			return dispatch(e, "status", false)
		},
	}
}

// dispatch routes a verb to the pattern-TYPE implementation.
func dispatch(e *engine.Engine, verb string, destroy bool) error {
	switch verb {
	case "build", "deploy":
		// Forward verbs run the pattern's pipeline up to their terminal stage
		// (so a `check`-first pipeline gates the build/deploy).
		return e.RunPipeline(verb)
	case "down":
		// reverse/observe verbs run their step blocks directly (not the pipeline).
		return e.Down(destroy)
	case "status":
		return e.Status()
	}
	return fmt.Errorf("unknown verb %q", verb)
}
