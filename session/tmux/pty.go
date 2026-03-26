package tmux

import (
	"io"
	"os/exec"
)

// PtyHandle represents a handle to a pseudo-terminal that supports reading,
// writing, closing, and resizing. On Unix this is backed by a PTY master fd
// from creack/pty. On Windows this wraps a ConPTY with separate I/O pipes.
type PtyHandle interface {
	io.ReadWriteCloser
	// SetSize resizes the pseudo-terminal to the given dimensions.
	SetSize(rows, cols uint16) error
}

// PtyFactory creates pseudo-terminals for running commands.
type PtyFactory interface {
	Start(cmd *exec.Cmd) (PtyHandle, error)
	Close()
}
