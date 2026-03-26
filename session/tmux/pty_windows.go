//go:build windows

package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procCreatePseudoConsole    = kernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole    = kernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole     = kernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAL = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttr   = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAL     = kernel32.NewProc("DeleteProcThreadAttributeList")
)

const _PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE = 0x00020016

// conPtyHandle wraps a Windows ConPTY with I/O pipes.
//
// Close ordering matters: the ConPTY handle must be closed BEFORE the I/O
// pipes, otherwise reads on outPipe will block forever waiting for EOF.
type conPtyHandle struct {
	inPipe  *os.File       // write to this to send input to the process
	outPipe *os.File       // read from this to get process output
	hPC     windows.Handle // pseudo console handle
	hProc   windows.Handle // process handle for wait/kill support

	closeOnce sync.Once
}

func (h *conPtyHandle) Read(p []byte) (int, error) {
	return h.outPipe.Read(p)
}

func (h *conPtyHandle) Write(p []byte) (int, error) {
	return h.inPipe.Write(p)
}

// Close tears down the ConPTY and its I/O pipes. It is safe to call multiple
// times. The ordering is critical:
//  1. Close the ConPTY handle — this signals EOF to the output pipe.
//  2. Close the input pipe.
//  3. Close the output pipe (reads will now return EOF).
func (h *conPtyHandle) Close() error {
	var firstErr error
	h.closeOnce.Do(func() {
		// Step 1: Close ConPTY handle first so the output pipe gets EOF.
		if h.hPC != 0 {
			procClosePseudoConsole.Call(uintptr(h.hPC))
			h.hPC = 0
		}

		// Step 2: Close input pipe.
		if h.inPipe != nil {
			if err := h.inPipe.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
			h.inPipe = nil
		}

		// Step 3: Close output pipe.
		if h.outPipe != nil {
			if err := h.outPipe.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
			h.outPipe = nil
		}

		// Step 4: Close the process handle.
		if h.hProc != 0 {
			windows.CloseHandle(h.hProc)
			h.hProc = 0
		}
	})
	return firstErr
}

func (h *conPtyHandle) SetSize(rows, cols uint16) error {
	if h.hPC == 0 {
		return fmt.Errorf("ConPTY handle is closed")
	}
	// COORD is packed as two int16 values: X (cols), Y (rows).
	coord := uintptr(cols) | (uintptr(rows) << 16)
	ret, _, err := procResizePseudoConsole.Call(uintptr(h.hPC), coord)
	if ret != 0 {
		return fmt.Errorf("ResizePseudoConsole failed: %w", err)
	}
	return nil
}

// windowsPty creates pseudo-terminals using Windows ConPTY.
type windowsPty struct{}

func (p *windowsPty) Start(cmd *exec.Cmd) (PtyHandle, error) {
	// Create pipes using os.Pipe() instead of windows.CreatePipe(). This is
	// critical: os.Pipe() returns Go-managed *os.File handles that are
	// properly integrated with the Go runtime's I/O poller (IOCP). Using
	// windows.CreatePipe() + os.NewFile() creates synchronous handles that
	// the runtime can't manage correctly, leading to blocked goroutines.
	//
	// Pipe layout:
	//   ptyIn  (read)  → ConPTY stdin  ← inPipeW (write) [we write here]
	//   ptyOut (write) → ConPTY stdout → outPipeR (read)  [we read here]
	ptyIn, inPipeW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("os.Pipe (stdin): %w", err)
	}
	outPipeR, ptyOut, err := os.Pipe()
	if err != nil {
		ptyIn.Close()
		inPipeW.Close()
		return nil, fmt.Errorf("os.Pipe (stdout): %w", err)
	}

	// Create the pseudo console with a default 80x24 size.
	// COORD is packed as X (cols) in low 16 bits, Y (rows) in high 16 bits.
	coord := uintptr(80) | (uintptr(24) << 16)
	var hPC windows.Handle
	ret, _, conErr := procCreatePseudoConsole.Call(
		coord,
		ptyIn.Fd(),
		ptyOut.Fd(),
		0, // flags: do NOT use PSEUDOCONSOLE_INHERIT_CURSOR (known hang bug)
		uintptr(unsafe.Pointer(&hPC)),
	)
	if ret != 0 {
		ptyIn.Close()
		inPipeW.Close()
		outPipeR.Close()
		ptyOut.Close()
		return nil, fmt.Errorf("CreatePseudoConsole failed (HRESULT 0x%08x): %w", ret, conErr)
	}

	// ConPTY duplicates the pipe handles internally. Close the PTY-side ends
	// now — ConPTY owns its copies. (This matches microsoft/hcsshim and
	// aymanbagabas/go-pty patterns.)
	ptyIn.Close()
	ptyOut.Close()

	// Start the process attached to the ConPTY.
	hProc, err := startProcessWithConPty(cmd, hPC)
	if err != nil {
		inPipeW.Close()
		outPipeR.Close()
		procClosePseudoConsole.Call(uintptr(hPC))
		return nil, err
	}

	return &conPtyHandle{
		inPipe:  inPipeW,
		outPipe: outPipeR,
		hPC:     hPC,
		hProc:   hProc,
	}, nil
}

func (p *windowsPty) Close() {}

// MakePtyFactory returns a PtyFactory appropriate for the current platform.
func MakePtyFactory() PtyFactory {
	return &windowsPty{}
}

// startProcessWithConPty starts a process attached to the given ConPTY handle.
// It returns the process handle so the caller can manage its lifecycle.
func startProcessWithConPty(cmd *exec.Cmd, hPC windows.Handle) (windows.Handle, error) {
	// Determine attribute list size. The first call always "fails" (returns FALSE)
	// but fills in the required size.
	// InitializeProcThreadAttributeList(lpAttributeList, dwAttributeCount, dwFlags, lpSize)
	// First call: lpAttributeList=NULL to query required size.
	var attrListSize uintptr
	procInitializeProcThreadAL.Call(0, 1, 0, uintptr(unsafe.Pointer(&attrListSize)))
	if attrListSize == 0 {
		return 0, fmt.Errorf("InitializeProcThreadAttributeList returned zero size")
	}

	attrList := make([]byte, attrListSize)
	attrListPtr := unsafe.Pointer(&attrList[0])

	// Second call: actually initialize with the allocated buffer.
	ret, _, err := procInitializeProcThreadAL.Call(
		uintptr(attrListPtr), 1, 0, uintptr(unsafe.Pointer(&attrListSize)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("InitializeProcThreadAttributeList: %w", err)
	}
	defer procDeleteProcThreadAL.Call(uintptr(attrListPtr))

	// Add the pseudo console attribute.
	ret, _, err = procUpdateProcThreadAttr.Call(
		uintptr(attrListPtr),
		0,
		_PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(hPC),
		unsafe.Sizeof(hPC),
		0, 0,
	)
	if ret == 0 {
		return 0, fmt.Errorf("UpdateProcThreadAttribute: %w", err)
	}

	// Build the command line. exec.Cmd populates Args[0] with Path, so we
	// always use the full Args slice for ComposeCommandLine.
	args := cmd.Args
	if len(args) == 0 {
		args = []string{cmd.Path}
	}
	cmdLine := windows.ComposeCommandLine(args)
	cmdLinePtr, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		return 0, err
	}

	var dirPtr *uint16
	if cmd.Dir != "" {
		dirPtr, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return 0, err
		}
	}

	// Build environment block if cmd.Env is set.
	var envBlock *uint16
	createFlags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT)
	if len(cmd.Env) > 0 {
		envBlock = createEnvBlock(cmd.Env)
		createFlags |= windows.CREATE_UNICODE_ENVIRONMENT
	}

	// Create the process with extended startup info.
	si := &windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = (*windows.ProcThreadAttributeList)(attrListPtr)

	var pi windows.ProcessInformation
	err = windows.CreateProcess(
		nil,
		cmdLinePtr,
		nil, nil,
		false,
		createFlags,
		envBlock,
		dirPtr,
		&si.StartupInfo,
		&pi,
	)
	if err != nil {
		return 0, fmt.Errorf("CreateProcess: %w", err)
	}

	// Close the thread handle; we don't need it.
	windows.CloseHandle(pi.Thread)

	// Wire the process into cmd.Process so callers can use cmd.Process.Kill()
	// and cmd.Process.Wait(). os.FindProcess on Windows does not open a new
	// handle — it just wraps the PID. We keep hProc for our own lifecycle
	// management.
	cmd.Process, _ = os.FindProcess(int(pi.ProcessId))

	return pi.Process, nil
}

// createEnvBlock builds a Windows environment block (null-terminated strings,
// double-null terminated) from a string slice.
func createEnvBlock(env []string) *uint16 {
	if len(env) == 0 {
		return nil
	}
	var block []uint16
	for _, s := range env {
		u := windows.StringToUTF16(s)
		block = append(block, u...)
	}
	block = append(block, 0) // double null terminator
	return &block[0]
}
