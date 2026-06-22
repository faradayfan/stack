package plugins

import "testing"

func TestLoad_BuiltinManifests(t *testing.T) {
	r, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docker", "grype", "helm", "kubectl"} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("built-in manifest %q not loaded", name)
		}
	}
}

func TestCommandFor_DockerVariantAndIncompatible(t *testing.T) {
	r, _ := Load()
	docker, _ := r.Get("docker")

	// A supported version resolves build-artifact.
	cmd, ok, err := docker.CommandFor("build-artifact", "27.1.1")
	if err != nil || !ok || cmd == "" {
		t.Fatalf("build-artifact@27.1.1 = %q ok=%v err=%v", cmd, ok, err)
	}

	// An incompatible version errors clearly.
	if _, _, err := docker.CommandFor("build-artifact", "18.0.0"); err == nil {
		t.Error("docker 18.0.0 should be incompatible")
	}
}

func TestCommandFor_VersionIndependentSteps(t *testing.T) {
	r, _ := Load()
	helm, _ := r.Get("helm")
	// helm uses top-level steps (no variants) → version is ignored.
	cmd, ok, err := helm.CommandFor("apply", "3.16.4")
	if err != nil || !ok || cmd == "" {
		t.Fatalf("helm apply = %q ok=%v err=%v", cmd, ok, err)
	}
}

func TestProvides(t *testing.T) {
	r, _ := Load()
	grype, _ := r.Get("grype")
	if !grype.ProvidesStep("scan-artifact") {
		t.Error("grype must provide scan-artifact")
	}
	if grype.ProvidesStep("apply") {
		t.Error("grype must not provide apply")
	}
}
