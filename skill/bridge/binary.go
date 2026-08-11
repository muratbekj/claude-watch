package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// findBinary mirrors server.js's findBinary(): check a fixed list of
// candidate paths for an executable file, then fall back to `which`.
func findBinary(name string, candidates []string) string {
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return c
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return ""
}

func findClaudeBinary() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", "claude"),
		"/usr/local/bin/claude",
		"/opt/homebrew/bin/claude",
	}
	bin := findBinary("claude", candidates)
	if bin == "" {
		logMsg("warn", "Could not find 'claude' binary — Claude sessions will not be available.")
	}
	return bin
}
