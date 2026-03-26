package tmux

import (
	cmd2 "claude-squad/cmd"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"claude-squad/cmd/cmd_test"

	"github.com/stretchr/testify/require"
)

// mockPtyHandle wraps an *os.File to implement PtyHandle for testing.
type mockPtyHandle struct {
	*os.File
}

func (m *mockPtyHandle) SetSize(rows, cols uint16) error {
	return nil
}

type MockPtyFactory struct {
	t *testing.T

	// Array of commands and the corresponding file handles representing PTYs.
	cmds    []*exec.Cmd
	files   []*os.File
	handles []PtyHandle
}

func (pt *MockPtyFactory) Start(cmd *exec.Cmd) (PtyHandle, error) {
	filePath := filepath.Join(pt.t.TempDir(), fmt.Sprintf("pty-%s-%d", pt.t.Name(), rand.Int31()))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	h := &mockPtyHandle{File: f}
	pt.cmds = append(pt.cmds, cmd)
	pt.files = append(pt.files, f)
	pt.handles = append(pt.handles, h)
	return h, nil
}

func (pt *MockPtyFactory) Close() {}

func NewMockPtyFactory(t *testing.T) *MockPtyFactory {
	return &MockPtyFactory{
		t: t,
	}
}

func TestSanitizeName(t *testing.T) {
	session := NewTmuxSession("asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf", session.sanitizedName)

	session = NewTmuxSession("a sd f . . asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf__asdf", session.sanitizedName)
}

func TestStartTmuxSession(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)

	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			cmdStr := cmd2.ToString(cmd)
			if strings.Contains(cmdStr, "tmux ls") {
				if !created {
					// First ls call: session doesn't exist yet.
					created = true
					return nil, fmt.Errorf("no server running")
				}
				// After creation: session exists.
				return []byte("claudesquad_test-session: 1 windows (created Thu Jan 1 00:00:00 2026)\n"), nil
			}
			return []byte("output"), nil
		},
	}

	workdir := t.TempDir()
	session := newTmuxSession("test-session", "claude", ptyFactory, cmdExec)

	err := session.Start(workdir)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Close the session's PTY handle so Windows can clean up TempDir.
		if session.ptmx != nil {
			session.ptmx.Close()
		}
	})
	require.Equal(t, 2, len(ptyFactory.cmds))
	require.Equal(t, fmt.Sprintf("tmux new-session -d -s claudesquad_test-session -c %s claude", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
	require.Equal(t, "tmux attach-session -t claudesquad_test-session",
		cmd2.ToString(ptyFactory.cmds[1]))

	require.Equal(t, 2, len(ptyFactory.files))

	// File should be closed.
	_, err = ptyFactory.files[0].Stat()
	require.Error(t, err)
	// File should be open
	_, err = ptyFactory.files[1].Stat()
	require.NoError(t, err)
}
