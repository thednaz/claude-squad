//go:build !windows

package config

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// getClaudeViaShell attempts to find the "claude" command by sourcing the
// user's shell profile and running "which". This handles cases where claude
// is installed via an alias or a PATH modification in .bashrc/.zshrc.
func getClaudeViaShell() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	var shellCmd string
	if strings.Contains(shell, "zsh") {
		shellCmd = "source ~/.zshrc &>/dev/null || true; which claude"
	} else if strings.Contains(shell, "bash") {
		shellCmd = "source ~/.bashrc &>/dev/null || true; which claude"
	} else {
		shellCmd = "which claude"
	}

	cmd := exec.Command(shell, "-c", shellCmd)
	output, err := cmd.Output()
	if err != nil || len(output) == 0 {
		return "", err
	}

	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", nil
	}

	// Handle alias definitions like "claude: aliased to /path/to/claude"
	aliasRegex := regexp.MustCompile(`(?:aliased to|->|=)\s*([^\s]+)`)
	matches := aliasRegex.FindStringSubmatch(path)
	if len(matches) > 1 {
		path = matches[1]
	}
	return path, nil
}

// DefaultShell returns the user's default shell on Unix.
func DefaultShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "/bin/sh"
	}
	return shell
}
