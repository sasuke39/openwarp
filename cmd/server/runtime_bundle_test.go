package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sasuke39/open-warp/internal/config"
)

func TestResolveBundledRuntimeMigratesKnownHarnessPath(t *testing.T) {
	contents := filepath.Join(t.TempDir(), "WarpLocal.app", "Contents")
	helpers := filepath.Join(contents, "Helpers")
	entry := filepath.Join(contents, "Resources", "dsh-runtime", "dist", "main.js")
	for _, path := range []string{filepath.Join(helpers, "node-runtime"), entry} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	command, args := resolveBundledRuntime(config.RuntimeConfig{
		Driver:  "deepseek-harness",
		Command: "/old/WarpLocal.app/Contents/Helpers/node-dsh",
		Args:    []string{"/old/WarpLocal.app/Contents/Resources/dsh-runtime/dist/main.js", "--flag"},
	}, filepath.Join(helpers, "warp-local-adapter"))

	if command != filepath.Join(helpers, "node-runtime") {
		t.Fatalf("command = %q", command)
	}
	if len(args) != 2 || args[0] != entry || args[1] != "--flag" {
		t.Fatalf("args = %#v", args)
	}
}

func TestResolveBundledRuntimeLeavesCustomDriverUntouched(t *testing.T) {
	command, args := resolveBundledRuntime(config.RuntimeConfig{
		Driver:  "custom",
		Command: "/usr/local/bin/custom-agent",
		Args:    []string{"serve"},
	}, "/Applications/WarpLocal.app/Contents/Helpers/warp-local-adapter")

	if command != "/usr/local/bin/custom-agent" || len(args) != 1 || args[0] != "serve" {
		t.Fatalf("unexpected custom runtime: command=%q args=%#v", command, args)
	}
}
