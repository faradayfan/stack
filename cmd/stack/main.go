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
	var dryRun bool

	root := &cobra.Command{
		Use:           "stack",
		Short:         "Deploy an app into a target environment from .stack/ context files",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&envFlag, "env", "", "environment to act on (overrides the current context)")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "print the commands that would run, without running them")

	// resolveEnv: the --env flag wins; else the repo's current-context.
	resolveEnv := func() (root string, env string, err error) {
		cwd, _ := os.Getwd()
		repo := config.FindRepoRoot(cwd)
		if envFlag != "" {
			return repo, envFlag, nil
		}
		st, err := config.LoadState(repo)
		if err != nil {
			return repo, "", err
		}
		if st.CurrentEnv == "" {
			return repo, "", fmt.Errorf("no current environment — run `stack use <env>` or pass --env")
		}
		return repo, st.CurrentEnv, nil
	}

	newEngine := func() (*engine.Engine, error) {
		repo, env, err := resolveEnv()
		if err != nil {
			return nil, err
		}
		cfg, err := config.Load(repo, env)
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
		envCmd(resolveEnv),
		deployCmd(newEngine),
		downCmd(newEngine),
		statusCmd(newEngine),
	)
	return root
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

func envCmd(resolveEnv func() (string, string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "env",
		Short: "Show the current environment and its resolved config",
		RunE: func(_ *cobra.Command, _ []string) error {
			repo, env, err := resolveEnv()
			if err != nil {
				return err
			}
			cfg, err := config.Load(repo, env)
			if err != nil {
				return err
			}
			fmt.Printf("environment : %s\n", cfg.EnvName)
			fmt.Printf("app         : %s\n", cfg.App.Name)
			fmt.Printf("pattern     : %s\n", cfg.Env.Pattern)
			fmt.Printf("kube_context: %s\n", cfg.Env.KubeContext)
			fmt.Printf("namespace   : %s\n", cfg.Env.Namespace)
			fmt.Printf("delivery    : %s\n", cfg.Env.ImageDelivery)
			fmt.Println("step → tool :")
			for _, step := range []string{"build-artifact", "deliver-artifact", "scan-artifact", "render-config", "apply", "wait-ready", "status"} {
				if t, ok := cfg.Env.Tools[step]; ok {
					fmt.Printf("  %-16s %s\n", step, t)
				}
			}
			return nil
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

// dispatch routes a verb to the pattern implementation. M1 ships `k8s`.
func dispatch(e *engine.Engine, verb string, destroy bool) error {
	switch e.Cfg.Env.Pattern {
	case "k8s":
		switch verb {
		case "deploy":
			return e.DeployK8s()
		case "down":
			return e.DownK8s(destroy)
		case "status":
			return e.StatusK8s()
		}
	default:
		return fmt.Errorf("pattern %q not implemented yet (M1 supports: k8s)", e.Cfg.Env.Pattern)
	}
	return fmt.Errorf("unknown verb %q", verb)
}
