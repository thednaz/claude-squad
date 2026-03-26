//go:build windows

package config

import (
	"os"
	"os/exec"
	"strings"
)

// getClaudeViaShell attempts to find the "claude" command using Windows-native
// methods. On Windows there is no $SHELL or shell profile sourcing — we use
// "where" (the Windows equivalent of "which") and check common install paths.
func getClaudeViaShell() (string, error) {
	// Try "where claude" which searches PATH on Windows.
	cmd := exec.Command("where", "claude")
	output, err := cmd.Output()
	if err != nil || len(output) == 0 {
		return "", err
	}

	// "where" can return multiple lines; take the first match.
	path := strings.TrimSpace(strings.Split(string(output), "\n")[0])
	return path, nil
}

// DefaultShell returns the default shell on Windows.
// Prefers PowerShell if available, falls back to cmd.exe.
func DefaultShell() string {
	// Check COMSPEC first (usually C:\WINDOWS\system32\cmd.exe)
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		// But prefer PowerShell if it's available
		if pwsh, err := exec.LookPath("pwsh"); err == nil {
			return pwsh // PowerShell 7+
		}
		if ps, err := exec.LookPath("powershell"); err == nil {
			return ps // Windows PowerShell 5.x
		}
		return comspec
	}
	return "cmd.exe"
}
