//go:build !windows

package tmux

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// unixPtyHandle wraps an *os.File PTY master with resize support via creack/pty.
type unixPtyHandle struct {
	*os.File
}

func (h *unixPtyHandle) SetSize(rows, cols uint16) error {
	return pty.Setsize(h.File, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

// unixPty creates pseudo-terminals using the creack/pty package.
type unixPty struct{}

func (p *unixPty) Start(cmd *exec.Cmd) (PtyHandle, error) {
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &unixPtyHandle{File: f}, nil
}

func (p *unixPty) Close() {}

// MakePtyFactory returns a PtyFactory appropriate for the current platform.
func MakePtyFactory() PtyFactory {
	return &unixPty{}
}
