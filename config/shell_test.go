package config

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultShell(t *testing.T) {
	shell := DefaultShell()
	require.NotEmpty(t, shell, "DefaultShell should return a non-empty string")
	t.Logf("DefaultShell() = %s", shell)

	if runtime.GOOS == "windows" {
		// On Windows, should be cmd.exe or powershell variant.
		require.Contains(t, shell, ".exe", "Windows shell should end in .exe")
	} else {
		// On Unix, should be an absolute path.
		require.True(t, shell[0] == '/', "Unix shell should be an absolute path, got: %s", shell)
	}
}

func TestGetClaudeViaShell(t *testing.T) {
	// This tests the platform-specific shell lookup. It's expected to fail
	// if claude isn't installed, so we just verify it doesn't panic.
	path, err := getClaudeViaShell()
	if err != nil {
		t.Logf("getClaudeViaShell returned error (expected if claude not installed): %v", err)
	} else {
		t.Logf("getClaudeViaShell returned: %s", path)
	}
}
