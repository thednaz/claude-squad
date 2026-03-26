//go:build windows

package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// =============================================================================
// Windows Smoke Tests
//
// These tests verify the Windows-specific code paths work correctly.
// Run with: go test -v -tags windows ./session/tmux/ -run TestWindows
//
// Prerequisites:
//   - Windows 10 1809+ (for ConPTY support)
//   - psmux installed and "tmux" alias on PATH
// =============================================================================

// --- ConPTY Unit Tests (no psmux required) ---

func TestWindowsConPtyCreateAndClose(t *testing.T) {
	// Verify ConPTY can be created and torn down without leaking handles.
	factory := MakePtyFactory()

	// Use cmd.exe /c echo as a simple test process.
	cmd := exec.Command("cmd.exe", "/c", "echo", "hello")
	handle, err := factory.Start(cmd)
	require.NoError(t, err, "ConPTY Start should succeed")
	require.NotNil(t, handle, "handle should not be nil")

	// Should be able to close cleanly.
	err = handle.Close()
	require.NoError(t, err, "ConPTY Close should succeed")

	// Double close should be safe (sync.Once).
	err = handle.Close()
	require.NoError(t, err, "double Close should be safe")
}

func TestWindowsConPtyResize(t *testing.T) {
	factory := MakePtyFactory()

	// Start a long-running process so we can resize.
	cmd := exec.Command("cmd.exe")
	handle, err := factory.Start(cmd)
	require.NoError(t, err)
	defer handle.Close()

	// Resize should succeed.
	err = handle.SetSize(40, 120)
	require.NoError(t, err, "SetSize should succeed")

	// Various sizes.
	for _, tc := range []struct{ rows, cols uint16 }{
		{24, 80},
		{50, 200},
		{1, 1},
	} {
		err = handle.SetSize(tc.rows, tc.cols)
		require.NoError(t, err, "SetSize(%d, %d) should succeed", tc.rows, tc.cols)
	}
}

func TestWindowsConPtyReadWrite(t *testing.T) {
	factory := MakePtyFactory()

	// Start cmd.exe so we can interact.
	cmd := exec.Command("cmd.exe")
	handle, err := factory.Start(cmd)
	require.NoError(t, err)
	defer handle.Close()

	// Write a command.
	_, err = handle.Write([]byte("echo TESTMARKER\r\n"))
	require.NoError(t, err, "Write should succeed")

	// Read output until we see our marker or timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	buf := make([]byte, 4096)
	var accumulated string
	found := false
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Timed out waiting for TESTMARKER in output. Got so far:\n%s", accumulated)
		default:
		}
		n, err := handle.Read(buf)
		if err != nil {
			break
		}
		accumulated += string(buf[:n])
		if strings.Contains(accumulated, "TESTMARKER") {
			found = true
			break
		}
	}
	require.True(t, found, "Should find TESTMARKER in ConPTY output")
}

func TestWindowsConPtyCloseSignalsEOF(t *testing.T) {
	factory := MakePtyFactory()

	cmd := exec.Command("cmd.exe")
	handle, err := factory.Start(cmd)
	require.NoError(t, err)

	// Start a reader goroutine.
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := handle.Read(buf)
			if err != nil {
				readDone <- err
				return
			}
		}
	}()

	// Close the handle — reader should get EOF.
	time.Sleep(100 * time.Millisecond) // let cmd.exe start
	handle.Close()

	select {
	case <-readDone:
		// Good — reader exited.
	case <-time.After(5 * time.Second):
		t.Fatal("Reader goroutine did not exit after Close (EOF not signaled)")
	}
}

// --- psmux Integration Tests (require psmux installed) ---

func TestWindowsPsmuxAvailable(t *testing.T) {
	// Check that psmux is installed and the "tmux" alias works.
	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("psmux not installed (tmux not on PATH) — skipping integration tests")
	}
	t.Logf("tmux found at: %s", path)

	// Verify it's actually psmux by checking version output.
	out, err := exec.Command("tmux", "-V").CombinedOutput()
	require.NoError(t, err, "tmux -V should succeed")
	t.Logf("tmux version: %s", strings.TrimSpace(string(out)))
}

func TestWindowsPsmuxSessionLifecycle(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("psmux not installed")
	}

	sessionName := fmt.Sprintf("cstest_%d", time.Now().UnixNano()%100000)
	fullName := TmuxPrefix + sessionName

	cmdExec := &realCmdExec{}
	session := NewTmuxSessionWithDeps(sessionName, "cmd.exe", MakePtyFactory(), cmdExec)

	// Create a temp workdir.
	workdir := t.TempDir()

	// Test 1: Session should not exist yet.
	require.False(t, session.DoesSessionExist(), "session should not exist before Start")

	// Test 2: Start the session.
	err := session.Start(workdir)
	require.NoError(t, err, "Start should succeed")
	t.Cleanup(func() { session.Close() })

	// Test 3: Session should exist now.
	require.True(t, session.DoesSessionExist(), "session should exist after Start")

	// Test 4: Capture pane content.
	content, err := session.CapturePaneContent()
	require.NoError(t, err, "CapturePaneContent should succeed")
	require.NotEmpty(t, content, "pane content should not be empty")
	t.Logf("Captured pane content (%d bytes):\n%.200s", len(content), content)

	// Test 5: Capture with options (scroll range).
	content2, err := session.CapturePaneContentWithOptions("-10", "")
	require.NoError(t, err, "CapturePaneContentWithOptions should succeed")
	t.Logf("Captured with options (%d bytes):\n%.200s", len(content2), content2)

	// Test 6: SetDetachedSize should work.
	err = session.SetDetachedSize(100, 40)
	require.NoError(t, err, "SetDetachedSize should succeed")

	// Test 7: HasUpdated should return without error.
	_, _ = session.HasUpdated()

	// Test 8: Close the session.
	err = session.Close()
	require.NoError(t, err, "Close should succeed")

	// Test 9: Session should be gone.
	existsCmd := exec.Command("tmux", "has-session", fmt.Sprintf("-t=%s", fullName))
	err = cmdExec.Run(existsCmd)
	require.Error(t, err, "session should not exist after Close")
}

func TestWindowsPsmuxSendKeys(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("psmux not installed")
	}

	sessionName := fmt.Sprintf("cskeys_%d", time.Now().UnixNano()%100000)
	cmdExec := &realCmdExec{}
	session := NewTmuxSessionWithDeps(sessionName, "cmd.exe", MakePtyFactory(), cmdExec)

	workdir := t.TempDir()
	err := session.Start(workdir)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })

	// Send "echo KEYTEST" via PTY write (simulates TapEnter/SendKeys path).
	err = session.SendKeys("echo KEYTEST\r\n")
	require.NoError(t, err, "SendKeys should succeed")

	// Wait a moment for the command to execute.
	time.Sleep(500 * time.Millisecond)

	// Capture and verify.
	content, err := session.CapturePaneContent()
	require.NoError(t, err)
	require.Contains(t, content, "KEYTEST", "pane should contain our echoed text")
	t.Logf("After SendKeys:\n%.300s", content)
}

func TestWindowsPsmuxANSICapture(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("psmux not installed")
	}

	sessionName := fmt.Sprintf("csansi_%d", time.Now().UnixNano()%100000)
	cmdExec := &realCmdExec{}
	session := NewTmuxSessionWithDeps(sessionName, "cmd.exe", MakePtyFactory(), cmdExec)

	workdir := t.TempDir()
	err := session.Start(workdir)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })

	// The -e flag in capture-pane preserves ANSI escape codes.
	// This is critical for claude-squad's diff display.
	content, err := session.CapturePaneContent()
	require.NoError(t, err)

	// Log whether ANSI escapes are present (ESC = 0x1b).
	hasAnsi := strings.Contains(content, "\x1b[")
	t.Logf("ANSI escapes present in capture: %v", hasAnsi)
	t.Logf("Raw content sample:\n%q", content[:min(len(content), 200)])
}

func TestWindowsPsmuxCleanupSessions(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("psmux not installed")
	}

	cmdExec := &realCmdExec{}

	// Create two sessions.
	s1Name := fmt.Sprintf("csclean1_%d", time.Now().UnixNano()%100000)
	s2Name := fmt.Sprintf("csclean2_%d", time.Now().UnixNano()%100000)
	session1 := NewTmuxSessionWithDeps(s1Name, "cmd.exe", MakePtyFactory(), cmdExec)
	session2 := NewTmuxSessionWithDeps(s2Name, "cmd.exe", MakePtyFactory(), cmdExec)

	workdir := t.TempDir()
	require.NoError(t, session1.Start(workdir))
	require.NoError(t, session2.Start(workdir))

	// Both should exist.
	require.True(t, session1.DoesSessionExist())
	require.True(t, session2.DoesSessionExist())

	// CleanupSessions should kill both.
	err := CleanupSessions(cmdExec)
	require.NoError(t, err, "CleanupSessions should succeed")

	// Both should be gone.
	require.False(t, session1.DoesSessionExist(), "session1 should be cleaned up")
	require.False(t, session2.DoesSessionExist(), "session2 should be cleaned up")
}

// --- Shell Detection Tests ---

func TestWindowsDefaultShell(t *testing.T) {
	// Imported via config package, but we can test the general principle:
	// On Windows, COMSPEC should be set.
	comspec := os.Getenv("COMSPEC")
	require.NotEmpty(t, comspec, "COMSPEC should be set on Windows")
	t.Logf("COMSPEC = %s", comspec)

	// Verify cmd.exe or powershell is findable.
	_, err := exec.LookPath("cmd.exe")
	require.NoError(t, err, "cmd.exe should be on PATH")
}

// --- Helpers ---

// realCmdExec is a real command executor for integration tests.
type realCmdExec struct{}

func (e *realCmdExec) Run(cmd *exec.Cmd) error {
	return cmd.Run()
}

func (e *realCmdExec) Output(cmd *exec.Cmd) ([]byte, error) {
	return cmd.Output()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
