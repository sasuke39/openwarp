package main

import (
	"os"
	"path/filepath"

	"github.com/sasuke39/open-warp/internal/config"
)

// resolveBundledRuntime keeps known harnesses attached to the App bundle that
// launched the adapter. This makes saved profiles survive app moves/upgrades.
func resolveBundledRuntime(runtimeCfg config.RuntimeConfig, executable string) (string, []string) {
	command := runtimeCfg.Command
	args := append([]string(nil), runtimeCfg.Args...)

	resourceDir := ""
	switch runtimeCfg.Driver {
	case "pi-agent":
		resourceDir = "pi-runtime"
	case "deepseek-harness":
		resourceDir = "dsh-runtime"
	default:
		return command, args
	}

	helpersDir := filepath.Dir(executable)
	if filepath.Base(helpersDir) != "Helpers" {
		return command, args
	}
	contentsDir := filepath.Dir(helpersDir)
	bundledCommand := filepath.Join(helpersDir, "node-runtime")
	bundledEntry := filepath.Join(contentsDir, "Resources", resourceDir, "dist", "main.js")
	if !regularFileExists(bundledCommand) || !regularFileExists(bundledEntry) {
		return command, args
	}

	if len(args) == 0 {
		args = []string{bundledEntry}
	} else {
		args[0] = bundledEntry
	}
	return bundledCommand, args
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
